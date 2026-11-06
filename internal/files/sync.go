package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/provider"
)

const (
	// syncGrace is how old an object has to be before it's added. An upload stores its
	// object a moment before saving its record, and adding it in that moment would make
	// the upload's own save fail, which would then delete the object.
	syncGrace = 10 * time.Minute
	// importedFallbackName stands in for a name dextro can't use.
	importedFallbackName = "Recovered file"
)

// SyncResult says what a sync changed. Skipped counts accounts it couldn't reach.
type SyncResult struct {
	Added   int
	Removed int
	Skipped int
}

// SyncConnection makes dextro's records match what's really in an account: files
// it has no record of come back, which is how a lost database recovers, and records
// of files that are gone are removed.
//
// Removing is the dangerous half, because a failure can look like an empty account.
// So nothing is removed unless the account answers for its folder, the listing
// reaches the end, and the account confirms that this one object is gone. This only
// removes records of objects already gone, so it doesn't break the rule that deleting
// is an explicit action.
func (s *Service) SyncConnection(ctx context.Context, userID, connectionID uuid.UUID, ip string) (SyncResult, error) {
	stored, err := s.providers.Provider(ctx, userID, connectionID)
	if err != nil {
		return SyncResult{}, err
	}
	// A trashed folder lists as empty, which must not read as everything deleted.
	if err := stored.Check(ctx); err != nil {
		return SyncResult{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	cutoff := time.Now().Add(-syncGrace)
	// Read before listing, so that everything here existed when the listing began.
	known, err := s.store.ObjectsForConnection(ctx, userID, connectionID)
	if err != nil {
		return SyncResult{}, err
	}

	var result SyncResult
	seen := make(map[string]bool, len(known))
	token := ""
	for {
		page, err := stored.List(ctx, token)
		if err != nil {
			// What was imported stays, and the next sync finishes the job.
			return result, errors.Join(fmt.Errorf("%w: %w", ErrProviderFailed, err), s.recordSync(ctx, userID, connectionID, ip, result))
		}
		for _, object := range page.Objects {
			seen[object.ID] = true
			if _, ok := known[object.ID]; ok || object.ModifiedAt.After(cutoff) {
				continue
			}
			added, err := s.importObject(ctx, userID, connectionID, object, false)
			if err != nil {
				return result, err
			}
			if added {
				result.Added++
			}
		}
		if token = page.NextPageToken; token == "" {
			break
		}
	}

	for objectID, fileID := range known {
		if seen[objectID] {
			continue
		}
		// A listing can lag behind a new file, so being missing from it proves nothing.
		exists, err := stored.Exists(ctx, objectID)
		if err != nil {
			return result, errors.Join(fmt.Errorf("%w: %w", ErrProviderFailed, err), s.recordSync(ctx, userID, connectionID, ip, result))
		}
		if exists {
			continue
		}
		err = s.store.DeleteFileForOwner(ctx, userID, fileID)
		if errors.Is(err, ErrFileNotFound) {
			continue
		}
		if err != nil {
			return result, err
		}
		result.Removed++
	}
	return result, s.recordSync(ctx, userID, connectionID, ip, result)
}

func (s *Service) importObject(ctx context.Context, userID, connectionID uuid.UUID, object provider.Object, external bool) (bool, error) {
	name, err := cleanName(object.Name)
	if err != nil {
		name = importedFallbackName
	}
	return s.store.ImportFile(ctx, userID, fileRecord{
		File: File{
			ConnectionID: connectionID,
			Name:         name,
			Size:         object.Size,
			ContentType:  cleanContentType(object.ContentType),
			External:     external,
		},
		objectID: object.ID,
	})
}

func (s *Service) recordSync(ctx context.Context, userID, connectionID uuid.UUID, ip string, result SyncResult) error {
	if result.Added == 0 && result.Removed == 0 {
		return nil
	}
	return s.store.Audit().Record(ctx, audit.Event{
		ActorID: userID,
		Action:  audit.ActionStorageSynced,
		IP:      ip,
		Metadata: map[string]any{
			"connection_id": connectionID.String(),
			"added":         result.Added,
			"removed":       result.Removed,
		},
	})
}

// SyncAll syncs every linked account. One that can't be reached is skipped: its
// token may have expired, which shouldn't stop anyone else's.
func (s *Service) SyncAll(ctx context.Context) (SyncResult, error) {
	accounts, err := s.providers.AllAccounts(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	var total SyncResult
	for _, account := range accounts {
		result, err := s.SyncConnection(ctx, account.UserID, account.ID, syncActor)
		total.Added += result.Added
		total.Removed += result.Removed
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		if err != nil {
			total.Skipped++
		}
	}
	return total, nil
}

// syncActor stands in for a client address on events from the background job.
const syncActor = "job"
