package files

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
	"github.com/swayam5342/tributary/internal/provider"
)

const (
	// maxPicked bounds one request, which makes a trip to the account per file.
	maxPicked = 100
	// nativeTypePrefix marks Google Docs, Sheets and the like, and folders. They have
	// no bytes to download.
	nativeTypePrefix = "application/vnd.google-apps."
)

// ErrInvalidImport means the list of files to add is empty or too long.
var ErrInvalidImport = errors.New("invalid import")

// ImportResult says what happened to the files someone picked.
type ImportResult struct {
	Added   int
	Skipped int
}

// ImportPicked adds files someone chose in Google's picker, which is what lets
// Tributary see them. They stay where they are in Drive. They're the person's own files
// rather than Tributary's, so they're marked External and deleting them only removes
// them from Tributary.
func (s *Service) ImportPicked(ctx context.Context, userID, connectionID uuid.UUID, ids []string, ip string) (ImportResult, error) {
	if len(ids) == 0 || len(ids) > maxPicked {
		return ImportResult{}, ErrInvalidImport
	}
	stored, err := s.providers.Provider(ctx, userID, connectionID)
	if err != nil {
		return ImportResult{}, err
	}
	var result ImportResult
	for _, id := range ids {
		added, err := s.importPicked(ctx, stored, userID, connectionID, id)
		if err != nil {
			return result, errors.Join(err, s.recordPicked(ctx, userID, connectionID, ip, result))
		}
		if added {
			result.Added++
		} else {
			result.Skipped++
		}
	}
	return result, s.recordPicked(ctx, userID, connectionID, ip, result)
}

func (s *Service) importPicked(ctx context.Context, stored provider.Provider, userID, connectionID uuid.UUID, id string) (bool, error) {
	object, err := stored.Describe(ctx, id)
	if errors.Is(err, provider.ErrObjectNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	if strings.HasPrefix(object.ContentType, nativeTypePrefix) {
		return false, nil
	}
	return s.importObject(ctx, userID, connectionID, object, nil, true)
}

func (s *Service) recordPicked(ctx context.Context, userID, connectionID uuid.UUID, ip string, result ImportResult) error {
	if result.Added == 0 {
		return nil
	}
	return s.store.Audit().Record(ctx, audit.Event{
		ActorID: userID,
		Action:  audit.ActionFilesAdded,
		IP:      ip,
		Metadata: map[string]any{
			"connection_id": connectionID.String(),
			"added":         result.Added,
		},
	})
}
