// Package storage manages the Google Drive accounts users link to Tributary.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
	"github.com/swayam5342/tributary/internal/config"
	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/provider/gdrive"
	"github.com/swayam5342/tributary/internal/secrets"
)

// ProviderKind says which service a connection uses.
type ProviderKind string

// ProviderGoogleDrive is the only service. The value is stored, so don't rename it.
const ProviderGoogleDrive ProviderKind = "google_drive"

const (
	checkTimeout    = 20 * time.Second
	secretContextID = "storage_connection:"
	uploadContextID = "upload_session:"
)

// Errors the handlers turn into stable API error codes.
var (
	ErrConnectionFailed   = errors.New("connection failed")
	ErrConnectionNotFound = errors.New("connection not found")
	// ErrConnectionHasLinks means removing an account would leave public links working
	// with nothing in Tributary to show them.
	ErrConnectionHasLinks = errors.New("connection has public links")
	ErrPickerDisabled     = errors.New("picker isn't configured")
)

// Connection is a linked account as its owner sees it. It holds no credentials,
// so it is always safe to send to the browser.
type Connection struct {
	ID        uuid.UUID
	Name      string
	Provider  ProviderKind
	Summary   string
	CreatedAt time.Time
	Draining  bool
}

// DriveAuth is Google's side of connecting a Drive account.
type DriveAuth interface {
	AuthCodeURL(state, verifier string) string
	Connect(ctx context.Context, code, verifier string) (gdrive.Settings, gdrive.Credentials, error)
}

// OpenDrive builds a provider from a connection's details.
type OpenDrive func(context.Context, gdrive.Settings, gdrive.Credentials) (provider.Provider, error)

// NewOpenDrive returns the real adapter. Drive may be nil if Google isn't
// configured.
func NewOpenDrive(drive *gdrive.Client) OpenDrive {
	return func(ctx context.Context, s gdrive.Settings, c gdrive.Credentials) (provider.Provider, error) {
		if drive == nil {
			return nil, errors.New("google isn't configured on this server")
		}
		return drive.Open(ctx, s, c)
	}
}

// Deps is everything a Service needs.
type Deps struct {
	Store     *Store
	Keyring   *secrets.Keyring
	OpenDrive OpenDrive
	// DriveAuth is nil when Google isn't configured.
	DriveAuth DriveAuth
	// Picker is empty when the picker isn't configured.
	Picker PickerConfig
}

// Service holds the rules for linked accounts.
type Service struct {
	store     *Store
	keyring   *secrets.Keyring
	openDrive OpenDrive
	driveAuth DriveAuth
	picker    PickerConfig
}

// NewService returns a Service.
func NewService(d Deps) *Service {
	return &Service{store: d.Store, keyring: d.Keyring, openDrive: d.OpenDrive, driveAuth: d.DriveAuth, picker: d.Picker}
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
func (s *Service) seal(id, userID uuid.UUID, name string, settings gdrive.Settings, credentials gdrive.Credentials) (record, error) {
	encodedSettings, err := json.Marshal(settings)
	if err != nil {
		return record{}, fmt.Errorf("encode settings: %w", err)
	}
	encodedCredentials, err := json.Marshal(credentials) //nolint:gosec // Goes straight into Encrypt below.
	if err != nil {
		return record{}, fmt.Errorf("encode credentials: %w", err)
	}
	return record{
		Connection: Connection{ID: id, Name: name, Provider: ProviderGoogleDrive},
		userID:     userID,
		settings:   encodedSettings,
		sealed:     s.keyring.Encrypt(encodedCredentials, secretContextID+id.String()),
	}, nil
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
			r, err := s.seal(uuid.New(), userID, name, settings, credentials)
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
		r, err := s.seal(existing.ID, userID, name, settings, credentials)
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
	return s.open(ctx, r)
}

func (s *Service) open(ctx context.Context, r record) (provider.Provider, error) {
	secret, err := s.keyring.Decrypt(r.sealed, secretContextID+r.ID.String())
	if err != nil {
		return nil, fmt.Errorf("decrypt connection %s: %w", r.ID, err)
	}
	var settings gdrive.Settings
	if err := json.Unmarshal(r.settings, &settings); err != nil {
		return nil, fmt.Errorf("decode settings: %w", err)
	}
	var credentials gdrive.Credentials
	// The decode error is dropped: JSON errors can quote what they failed on.
	if json.Unmarshal(secret, &credentials) != nil {
		return nil, errors.New("decode credentials: malformed value")
	}
	return s.openDrive(ctx, settings, credentials)
}

// SealUploadSession encrypts a session's address, bound to that upload so the value
// can't be moved onto another row.
func (s *Service) SealUploadSession(id uuid.UUID, address string) secrets.Sealed {
	return s.keyring.Encrypt([]byte(address), uploadContextID+id.String())
}

// OpenUploadSession decrypts a session's address.
func (s *Service) OpenUploadSession(id uuid.UUID, sealed secrets.Sealed) (string, error) {
	address, err := s.keyring.Decrypt(sealed, uploadContextID+id.String())
	if err != nil {
		return "", fmt.Errorf("decrypt upload %s: %w", id, err)
	}
	return string(address), nil
}

// SealFileKey encrypts the key of an encrypted file with the server's keyring.
func (s *Service) SealFileKey(key []byte, context string) secrets.Sealed {
	return s.keyring.Encrypt(key, context)
}

// OpenFileKey decrypts the key of an encrypted file.
func (s *Service) OpenFileKey(sealed secrets.Sealed, context string) ([]byte, error) {
	key, err := s.keyring.Decrypt(sealed, context)
	if err != nil {
		return nil, fmt.Errorf("decrypt file key: %w", err)
	}
	return key, nil
}

// PickerConfig is what Google's picker needs from this server's Cloud project.
type PickerConfig struct {
	APIKey string
	AppID  string
}

// PickerAccess is what the browser needs to run Google's picker on one account.
type PickerAccess struct {
	AccessToken  string
	DeveloperKey string
	AppID        string
}

// PickerEnabled reports whether files can be added with Google's picker.
func (s *Service) PickerEnabled() bool {
	return s.picker.APIKey != "" && s.picker.AppID != ""
}

// OpenPicker hands the browser a short-lived token for one account, because the
// picker runs there. That's a credential leaving the server, so it's recorded.
func (s *Service) OpenPicker(ctx context.Context, userID, id uuid.UUID, ip string) (PickerAccess, error) {
	if !s.PickerEnabled() {
		return PickerAccess{}, ErrPickerDisabled
	}
	opened, err := s.Provider(ctx, userID, id)
	if err != nil {
		return PickerAccess{}, err
	}
	token, err := opened.AccessToken(ctx)
	if err != nil {
		return PickerAccess{}, fmt.Errorf("%w: %w", ErrConnectionFailed, err)
	}
	err = s.store.Audit().Record(ctx, audit.Event{
		ActorID:  userID,
		Action:   audit.ActionPickerOpened,
		IP:       ip,
		Metadata: map[string]any{"connection_id": id.String()},
	})
	if err != nil {
		return PickerAccess{}, err
	}
	return PickerAccess{AccessToken: token, DeveloperKey: s.picker.APIKey, AppID: s.picker.AppID}, nil
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
	links, err := s.store.CountSharesForConnection(ctx, userID, id)
	if err != nil {
		return err
	}
	if links > 0 {
		r, err := s.store.GetConnectionForOwner(ctx, userID, id)
		if err != nil {
			return err
		}
		// Links can't be removed from an account that doesn't answer, and that mustn't
		// trap someone with a dead account. The audit log keeps how many were left.
		if s.probe(ctx, r).err == nil {
			return ErrConnectionHasLinks
		}
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteConnectionForOwner(ctx, userID, id); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionStorageRemoved,
			IP:       ip,
			Metadata: map[string]any{"connection_id": id, "links_left": links},
		})
	})
}

func withSummary(r record) (Connection, error) {
	var settings gdrive.Settings
	if err := json.Unmarshal(r.settings, &settings); err != nil {
		return Connection{}, fmt.Errorf("decode settings: %w", err)
	}
	connection := r.Connection
	connection.Summary = settings.Email
	return connection, nil
}

// FromConfig builds the service as the server and the admin's command line both need it.
// Google's parts stay nil when it isn't configured, which turns its routes off.
func FromConfig(cfg config.Config, db database) (*Service, error) {
	keyring, err := secrets.NewKeyring(cfg.EncryptionKeys, cfg.ActiveKeyID)
	if err != nil {
		return nil, err
	}
	var drive *gdrive.Client
	var driveAuth DriveAuth
	if cfg.GoogleEnabled() {
		drive = gdrive.NewClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.PublicURL+"/api/v1/storage/google/callback")
		driveAuth = drive
	}
	// The app ID is the Cloud project number, which starts every client ID.
	project, _, _ := strings.Cut(cfg.GoogleClientID, "-")
	return NewService(Deps{
		Store:     NewStore(db),
		Keyring:   keyring,
		OpenDrive: NewOpenDrive(drive),
		DriveAuth: driveAuth,
		Picker:    PickerConfig{APIKey: cfg.GooglePickerAPIKey, AppID: project},
	}), nil
}
