package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
	"github.com/swayam5342/tributary/internal/provider"
)

// abandonedShareAfter is how long a link can sit unconfirmed by Google before it's
// taken for the leftovers of a crash. Asking Google takes seconds.
const abandonedShareAfter = 5 * time.Minute

// Errors for Google links.
var (
	ErrLinksDisabled = errors.New("google links are turned off")
	ErrInvalidShare  = errors.New("invalid share")
	ErrShareNotFound = errors.New("share not found")
	// ErrFileMoving means a file is on its way to another account, where it'll have a
	// different address, so a link made now would stop working.
	ErrFileMoving = errors.New("file is moving between accounts")
	// ErrFileShared is the same thing the other way round.
	ErrFileShared = errors.New("file has a google link")
)

// shareRoles maps what the app calls a role to what the account does.
var shareRoles = map[string]provider.ShareRole{
	"viewer":    provider.ShareViewer,
	"commenter": provider.ShareCommenter,
	"editor":    provider.ShareEditor,
}

// Share is a public Google link on one file. Anyone who has URL can reach the file,
// with no sign-in, until the link is removed.
type Share struct {
	FileID    uuid.UUID
	Name      string
	Role      string
	URL       string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// ShareList is a user's links, and whether they can make more.
type ShareList struct {
	Enabled bool
	Shares  []Share
}

// ShareInput is what someone chose for a link. A nil ExpiresAt means it stays until
// it's removed.
type ShareInput struct {
	Role      string
	ExpiresAt *time.Time
}

// ShareRun says what one run of the expiry job did.
type ShareRun struct {
	Expired  int
	Failures []error
}

// ListShares returns a user's links.
func (s *Service) ListShares(ctx context.Context, userID uuid.UUID) (ShareList, error) {
	enabled, err := s.store.Settings().GoogleLinksEnabled(ctx)
	if err != nil {
		return ShareList{}, err
	}
	shares, err := s.store.SharesForOwner(ctx, userID)
	if err != nil {
		return ShareList{}, err
	}
	return ShareList{Enabled: enabled, Shares: shares}, nil
}

// ShareFile makes a public link, or changes one. This is the only way anything in
// Tributary becomes public, and it's one file at a time, on its owner's say-so. The row is
// written before Google is asked, so a crash can't leave a public link nothing knows
// about.
func (s *Service) ShareFile(ctx context.Context, userID, id uuid.UUID, in ShareInput, ip string) (Share, error) {
	role, ok := shareRoles[in.Role]
	if !ok || (in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now())) {
		return Share{}, ErrInvalidShare
	}
	enabled, err := s.store.Settings().GoogleLinksEnabled(ctx)
	if err != nil {
		return Share{}, err
	}
	if !enabled {
		return Share{}, ErrLinksDisabled
	}
	record, err := liveFile(ctx, s.store, userID, id)
	if err != nil {
		return Share{}, err
	}
	// A person's own Drive file may already have a link they made, and removing Tributary's
	// would take theirs with it. Google's own sharing is the place for those.
	if record.External {
		return Share{}, ErrExternalFile
	}
	// Google would be handing out encrypted bytes that nobody could open.
	if record.Encrypted {
		return Share{}, ErrFileEncrypted
	}
	// A file in a trashed folder is in the trash too, whatever its own flag says.
	if _, err := requireLiveFolder(ctx, s.store, userID, record.FolderID); err != nil {
		return Share{}, err
	}
	moving, err := s.store.IsFileWaitingToMove(ctx, userID, id)
	if err != nil {
		return Share{}, err
	}
	if moving {
		return Share{}, ErrFileMoving
	}
	stored, err := s.openAccount(ctx, userID, record.ConnectionID)
	if err != nil {
		return Share{}, err
	}
	existing, found, err := s.store.ShareForOwner(ctx, userID, id)
	if err != nil {
		return Share{}, err
	}
	finished := found && existing.URL != ""
	if err := s.store.PrepareShare(ctx, userID, id, in); err != nil {
		return Share{}, err
	}

	url, err := stored.Share(ctx, record.objectID, role)
	if errors.Is(err, provider.ErrShareRefused) && !finished {
		// Google said no, so there's nothing public to keep track of.
		return Share{}, errors.Join(err, s.store.DeleteShareForOwner(ctx, userID, id))
	}
	if err != nil {
		// Anything else may have worked, so the row stays for the job to clear up.
		return Share{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}

	saved, err := s.store.SaveShare(ctx, userID, id, in, url)
	if errors.Is(err, ErrShareNotFound) {
		// Trashed or removed while Google was answering, and it mustn't stay public.
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		return Share{}, errors.Join(ErrFileNotFound, stored.Unshare(cleanup, record.objectID))
	}
	if err != nil {
		return Share{}, err
	}
	saved.Name = record.Name
	action := audit.ActionFileShared
	if finished {
		action = audit.ActionShareChanged
	}
	return saved, s.store.Audit().Record(ctx, audit.Event{
		ActorID:  userID,
		Action:   action,
		IP:       ip,
		Metadata: map[string]any{"file_id": id.String(), "role": in.Role, "expires_at": in.ExpiresAt},
	})
}

// UnshareFile removes a link.
func (s *Service) UnshareFile(ctx context.Context, userID, id uuid.UUID, ip string) error {
	found, err := s.revokeShare(ctx, userID, id, ip)
	if err == nil && !found {
		return ErrShareNotFound
	}
	return err
}

// endShare ends a link. Google goes first, so a link that's still working never
// disappears from the page that lists them.
func (s *Service) endShare(ctx context.Context, stored provider.Provider, userID, fileID uuid.UUID, objectID string) error {
	if err := stored.Unshare(ctx, objectID); err != nil {
		return fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	if err := s.store.DeleteShareForOwner(ctx, userID, fileID); err != nil && !errors.Is(err, ErrShareNotFound) {
		return err
	}
	return nil
}

// revokeShare ends a link if the file has one, and says whether it did.
func (s *Service) revokeShare(ctx context.Context, userID, id uuid.UUID, ip string) (bool, error) {
	if _, found, err := s.store.ShareForOwner(ctx, userID, id); err != nil || !found {
		return false, err
	}
	record, err := s.store.FileForOwner(ctx, userID, id)
	if err != nil {
		return false, err
	}
	stored, err := s.openAccount(ctx, userID, record.ConnectionID)
	if err != nil {
		return false, err
	}
	if err := s.endShare(ctx, stored, userID, id, record.objectID); err != nil {
		return false, err
	}
	return true, s.store.Audit().Record(ctx, audit.Event{
		ActorID:  userID,
		Action:   audit.ActionFileUnshared,
		IP:       ip,
		Metadata: map[string]any{"file_id": id.String()},
	})
}

// revokeSharesUnderFolder ends every link inside a folder, however deep. Its contents
// travel with it into the trash, but the trash exists only here, so without this their
// links would keep working.
func (s *Service) revokeSharesUnderFolder(ctx context.Context, userID, folderID uuid.UUID, ip string) error {
	ids, err := s.store.SharedFilesUnderFolder(ctx, userID, folderID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.revokeShare(ctx, userID, id, ip); err != nil {
			return err
		}
	}
	return nil
}

// ExpireShares removes links past their expiry, because Google has no expiry for links
// like these, and clears up after crashes. It's the background job's, so it goes through
// every user's links. A link that can't be removed stays listed and is tried again.
func (s *Service) ExpireShares(ctx context.Context) (ShareRun, error) {
	due, err := s.store.DueShares(ctx, time.Now().Add(-abandonedShareAfter))
	if err != nil {
		return ShareRun{}, err
	}
	var run ShareRun
	// One failure means the rest of that account's links would fail too, and each one
	// that's stuck goes to the back so it can't keep working links from their turn.
	stuck := map[uuid.UUID]bool{}
	for _, share := range due {
		var err error
		if !stuck[share.connectionID] {
			err = s.expireShare(ctx, share)
		}
		if ctx.Err() != nil {
			return run, ctx.Err()
		}
		if err == nil && !stuck[share.connectionID] {
			run.Expired++
			continue
		}
		if err != nil {
			stuck[share.connectionID] = true
			run.Failures = append(run.Failures, fmt.Errorf("link on file %s: %w", share.fileID, err))
		}
		if err := s.store.DelayShare(ctx, share.fileID); err != nil {
			return run, err
		}
	}
	return run, nil
}

func (s *Service) expireShare(ctx context.Context, share dueShare) error {
	stored, err := s.providers.Provider(ctx, share.userID, share.connectionID)
	if err != nil {
		return err
	}
	if err := s.endShare(ctx, stored, share.userID, share.fileID, share.objectID); err != nil {
		return err
	}
	if share.unfinished {
		return nil
	}
	return s.store.Audit().Record(ctx, audit.Event{
		ActorID:  share.userID,
		Action:   audit.ActionShareExpired,
		IP:       jobActor,
		Metadata: map[string]any{"file_id": share.fileID.String()},
	})
}
