// Package storage manages the storage accounts users connect to dextro.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/netguard"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/provider/gdrive"
	"github.com/swayam5342/dextro/internal/provider/s3"
	"github.com/swayam5342/dextro/internal/secrets"
)

// ProviderKind says which service a connection uses.
type ProviderKind string

// Services a connection can use. These are stored, so don't rename them.
const (
	ProviderS3          ProviderKind = "s3"
	ProviderGoogleDrive ProviderKind = "google_drive"
)

const (
	defaultRegion   = "us-east-1"
	maxNameLength   = 100
	maxKeyLength    = 512
	checkTimeout    = 20 * time.Second
	secretContextID = "storage_connection:"
)

var (
	bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	regionPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)
)

// Errors the handlers turn into stable API error codes.
var (
	ErrInvalidConnection  = errors.New("invalid connection details")
	ErrEndpointNotAllowed = errors.New("endpoint not allowed")
	ErrConnectionFailed   = errors.New("connection failed")
	ErrConnectionNotFound = errors.New("connection not found")
	ErrNameTaken          = errors.New("connection name taken")
)

// Connection is a storage account as its owner sees it. It holds no credentials,
// so it is always safe to send to the browser.
type Connection struct {
	ID        uuid.UUID
	Name      string
	Provider  ProviderKind
	Summary   string
	CreatedAt time.Time
}

// S3Input is what a user provides to connect a bucket.
type S3Input struct {
	Name            string
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
}

// EndpointChecker validates user-supplied endpoints.
type EndpointChecker interface {
	CheckEndpoint(ctx context.Context, raw string) (string, error)
}

// DriveAuth is Google's side of connecting a Drive account.
type DriveAuth interface {
	AuthCodeURL(state, verifier string) string
	Connect(ctx context.Context, code, verifier string) (gdrive.Settings, gdrive.Credentials, error)
}

// Openers build a provider from a connection's details.
type Openers struct {
	S3    func(s3.Settings, s3.Credentials) (provider.Provider, error)
	Drive func(context.Context, gdrive.Settings, gdrive.Credentials) (provider.Provider, error)
}

// NewOpeners returns the real adapters. S3 connections go through the guard
// because their endpoints are user-supplied. Drive may be nil if Google isn't
// configured.
func NewOpeners(guard *netguard.Guard, drive *gdrive.Client) Openers {
	return Openers{
		S3: func(s s3.Settings, c s3.Credentials) (provider.Provider, error) {
			return s3.Open(s, c, guard.HTTPClient())
		},
		Drive: func(ctx context.Context, s gdrive.Settings, c gdrive.Credentials) (provider.Provider, error) {
			if drive == nil {
				return nil, errors.New("google isn't configured on this server")
			}
			return drive.Open(ctx, s, c)
		},
	}
}

// Deps is everything a Service needs.
type Deps struct {
	Store   *Store
	Keyring *secrets.Keyring
	Guard   EndpointChecker
	Openers Openers
	// DriveAuth is nil when Google isn't configured.
	DriveAuth DriveAuth
}

// Service holds the rules for storage connections.
type Service struct {
	store     *Store
	keyring   *secrets.Keyring
	guard     EndpointChecker
	openers   Openers
	driveAuth DriveAuth
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	return &Service{store: d.Store, keyring: d.Keyring, guard: d.Guard, openers: d.Openers, driveAuth: d.DriveAuth}
}

// DriveEnabled reports whether Google Drive can be connected.
func (s *Service) DriveEnabled() bool {
	return s.driveAuth != nil
}

// DriveAuthURL returns where to send the browser to connect a Drive account.
func (s *Service) DriveAuthURL(state, verifier string) string {
	return s.driveAuth.AuthCodeURL(state, verifier)
}

// List returns a user's connections.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Connection, error) {
	records, err := s.store.ListConnectionsForOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	connections := make([]Connection, 0, len(records))
	for _, r := range records {
		connection, err := withSummary(r)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	return connections, nil
}

// CreateS3 connects a bucket. The details are tried first, so a saved connection
// is one that worked. The endpoint is checked before anything contacts it.
func (s *Service) CreateS3(ctx context.Context, userID uuid.UUID, in S3Input, ip string) (Connection, error) {
	name, err := validateS3(&in)
	if err != nil {
		return Connection{}, err
	}
	endpoint, err := s.guard.CheckEndpoint(ctx, in.Endpoint)
	if err != nil {
		return Connection{}, fmt.Errorf("%w: %w", ErrEndpointNotAllowed, err)
	}

	settings := s3.Settings{Endpoint: endpoint, Region: in.Region, Bucket: in.Bucket}
	credentials := s3.Credentials{AccessKeyID: in.AccessKeyID, SecretAccessKey: in.SecretAccessKey}
	opened, err := s.openers.S3(settings, credentials)
	if err != nil {
		return Connection{}, fmt.Errorf("open s3 connection: %w", err)
	}
	if err := check(ctx, opened); err != nil {
		return Connection{}, err
	}

	r, err := s.seal(uuid.New(), userID, ProviderS3, name, settings, credentials)
	if err != nil {
		return Connection{}, err
	}
	return s.create(ctx, r, ip)
}

func validateS3(in *S3Input) (string, error) {
	name, err := validateName(in.Name)
	if err != nil {
		return "", err
	}
	if in.Region == "" {
		in.Region = defaultRegion
	}
	if !bucketPattern.MatchString(in.Bucket) || !regionPattern.MatchString(in.Region) {
		return "", ErrInvalidConnection
	}
	if in.AccessKeyID == "" || in.SecretAccessKey == "" ||
		len(in.AccessKeyID) > maxKeyLength || len(in.SecretAccessKey) > maxKeyLength {
		return "", ErrInvalidConnection
	}
	return name, nil
}

func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > maxNameLength || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidConnection
	}
	return name, nil
}

func check(ctx context.Context, opened provider.Provider) error {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	if err := opened.Check(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrConnectionFailed, err)
	}
	return nil
}

// seal encrypts credentials, bound to this connection's ID so that the value
// can't be moved onto another row.
func (s *Service) seal(id, userID uuid.UUID, kind ProviderKind, name string, settings, credentials any) (record, error) {
	encodedSettings, err := json.Marshal(settings)
	if err != nil {
		return record{}, fmt.Errorf("encode settings: %w", err)
	}
	encodedCredentials, err := json.Marshal(credentials)
	if err != nil {
		return record{}, fmt.Errorf("encode credentials: %w", err)
	}
	return record{
		Connection: Connection{ID: id, Name: name, Provider: kind},
		userID:     userID,
		settings:   encodedSettings,
		sealed:     s.keyring.Encrypt(encodedCredentials, secretContextID+id.String()),
	}, nil
}

func (s *Service) create(ctx context.Context, r record, ip string) (Connection, error) {
	var saved record
	err := s.store.InTx(ctx, func(tx *Store) error {
		var err error
		if saved, err = tx.CreateConnection(ctx, r); err != nil {
			return err
		}
		return recordConnected(ctx, tx, saved, ip)
	})
	if err != nil {
		return Connection{}, err
	}
	return withSummary(saved)
}

func recordConnected(ctx context.Context, tx *Store, r record, ip string) error {
	return tx.Audit().Record(ctx, audit.Event{
		ActorID:  r.userID,
		Action:   audit.ActionStorageConnected,
		IP:       ip,
		Metadata: map[string]any{"connection_id": r.ID, "provider": r.Provider},
	})
}

// ConnectDrive finishes connecting a Drive account. Connecting an account that's
// already connected replaces its token, which is how a revoked or expired
// connection is repaired.
func (s *Service) ConnectDrive(ctx context.Context, userID uuid.UUID, code, verifier, ip string) (Connection, error) {
	settings, credentials, err := s.driveAuth.Connect(ctx, code, verifier)
	if err != nil {
		return Connection{}, fmt.Errorf("%w: %w", ErrConnectionFailed, err)
	}
	name := "Google Drive (" + settings.Email + ")"

	var saved record
	err = s.store.InTx(ctx, func(tx *Store) error {
		existing, err := tx.GetConnectionByNameForOwner(ctx, userID, name)
		if errors.Is(err, ErrConnectionNotFound) {
			r, err := s.seal(uuid.New(), userID, ProviderGoogleDrive, name, settings, credentials)
			if err != nil {
				return err
			}
			if saved, err = tx.CreateConnection(ctx, r); err != nil {
				return err
			}
			return recordConnected(ctx, tx, saved, ip)
		}
		if err != nil {
			return err
		}
		if existing.Provider != ProviderGoogleDrive {
			return ErrNameTaken
		}
		r, err := s.seal(existing.ID, userID, ProviderGoogleDrive, name, settings, credentials)
		if err != nil {
			return err
		}
		if saved, err = tx.UpdateConnectionForOwner(ctx, r); err != nil {
			return err
		}
		return recordConnected(ctx, tx, saved, ip)
	})
	if err != nil {
		return Connection{}, err
	}
	return withSummary(saved)
}

// Provider opens one of a user's connections.
func (s *Service) Provider(ctx context.Context, userID, id uuid.UUID) (provider.Provider, error) {
	r, err := s.store.GetConnectionForOwner(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	secret, err := s.keyring.Decrypt(r.sealed, secretContextID+r.ID.String())
	if err != nil {
		return nil, fmt.Errorf("decrypt connection %s: %w", r.ID, err)
	}
	switch r.Provider {
	case ProviderS3:
		settings, credentials, err := decodeDetails[s3.Settings, s3.Credentials](r.settings, secret)
		if err != nil {
			return nil, err
		}
		return s.openers.S3(settings, credentials)
	case ProviderGoogleDrive:
		settings, credentials, err := decodeDetails[gdrive.Settings, gdrive.Credentials](r.settings, secret)
		if err != nil {
			return nil, err
		}
		return s.openers.Drive(ctx, settings, credentials)
	}
	return nil, fmt.Errorf("unknown provider %q", r.Provider)
}

func decodeDetails[S, C any](rawSettings, rawCredentials []byte) (S, C, error) {
	var settings S
	var credentials C
	if err := json.Unmarshal(rawSettings, &settings); err != nil {
		return settings, credentials, fmt.Errorf("decode settings: %w", err)
	}
	// The decode error is dropped: JSON errors can quote what they failed on.
	if json.Unmarshal(rawCredentials, &credentials) != nil {
		return settings, credentials, errors.New("decode credentials: malformed value")
	}
	return settings, credentials, nil
}

// Test checks that a connection still works.
func (s *Service) Test(ctx context.Context, userID, id uuid.UUID) error {
	opened, err := s.Provider(ctx, userID, id)
	if err != nil {
		return err
	}
	return check(ctx, opened)
}

// Delete removes a connection and forgets its credentials. It doesn't touch the
// account's files, which belong to the user wherever they are.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID, ip string) error {
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteConnectionForOwner(ctx, userID, id); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionStorageRemoved,
			IP:       ip,
			Metadata: map[string]any{"connection_id": id},
		})
	})
}

func withSummary(r record) (Connection, error) {
	connection := r.Connection
	switch r.Provider {
	case ProviderS3:
		var settings s3.Settings
		if err := json.Unmarshal(r.settings, &settings); err != nil {
			return Connection{}, fmt.Errorf("decode settings: %w", err)
		}
		host := settings.Endpoint
		if u, err := url.Parse(settings.Endpoint); err == nil {
			host = u.Host
		}
		connection.Summary = settings.Bucket + " on " + host
	case ProviderGoogleDrive:
		var settings gdrive.Settings
		if err := json.Unmarshal(r.settings, &settings); err != nil {
			return Connection{}, fmt.Errorf("decode settings: %w", err)
		}
		connection.Summary = settings.Email
	}
	return connection, nil
}
