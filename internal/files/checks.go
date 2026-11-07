package files

import (
	"cmp"
	"context"
	"crypto/md5" //nolint:gosec // Drive's own checksum, which is what a check compares.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/provider"
)

const (
	// checkBudget is how much a run reads, so that a check never competes with someone
	// using the app for long.
	checkBudget int64 = 1 << 30
	// checkBatch is how many files a run looks at, for a pool of mostly small files.
	checkBatch = 500
)

// ProblemKind is what's wrong with a file.
type ProblemKind string

// The problems a file can have.
const (
	// ProblemMissing means the file left its account, and with it the app.
	ProblemMissing ProblemKind = "missing"
	// ProblemChanged means the account reports different content than it stored.
	ProblemChanged ProblemKind = "changed"
	// ProblemDamaged means the bytes read back don't match the account's own checksum.
	ProblemDamaged ProblemKind = "damaged"
)

// FileProblem is something a sync or a check found. FileID is nil for a missing file.
type FileProblem struct {
	ID           uuid.UUID
	FileID       *uuid.UUID
	ConnectionID uuid.UUID
	Name         string
	Size         int64
	Kind         ProblemKind
	FoundAt      time.Time
}

// FileChecks is how the checks are going for one user.
type FileChecks struct {
	Enabled   bool
	Checkable int64
	Checked   int64
	Problems  []FileProblem
}

// CheckRun says what one run of the check found.
type CheckRun struct {
	Checked  int
	Damaged  int
	Failures []error
}

// checkItem is a file and whose it is, for the background check.
type checkItem struct {
	fileRecord
	userID uuid.UUID
}

// compareChecksums uses the listing a sync already has, so it costs nothing extra. Files
// from before checksums get theirs here. A file added from Drive is someone's own to
// change, so its checksum just follows along.
func (r *syncRun) compareChecksums(ctx context.Context, known map[string]fileRecord) error {
	store := r.s.store
	for objectID, record := range known {
		object, seen := r.found.files[objectID]
		if !seen || object.Checksum == "" || object.Checksum == record.checksum {
			continue
		}
		if record.checksum == "" || record.External {
			if err := store.SetFileChecksumForObject(ctx, r.userID, record.ID, r.connectionID, objectID, object.Checksum); err != nil {
				return err
			}
			continue
		}
		problem := FileProblem{FileID: &record.ID, ConnectionID: r.connectionID, Name: record.Name, Size: record.Size, Kind: ProblemChanged}
		if err := store.RecordFileProblem(ctx, r.userID, problem, object.Checksum); err != nil {
			return err
		}
	}
	return nil
}

// CheckFiles reads back the files that have waited longest, and compares their bytes with
// the checksum the account gave when they were stored. A sync already notices a file that's
// gone or replaced, so reading the bytes is the part only this can do. A file that can't be
// reached right now isn't damaged, so that's only a failure for the logs.
func (s *Service) CheckFiles(ctx context.Context) (CheckRun, error) {
	enabled, err := s.store.Settings().FileChecksEnabled(ctx)
	if err != nil || !enabled {
		return CheckRun{}, err
	}
	items, err := s.store.FilesToCheck(ctx, checkBatch)
	if err != nil {
		return CheckRun{}, err
	}
	var run CheckRun
	var read int64
	for _, item := range items {
		if read >= checkBudget {
			break
		}
		if err := ctx.Err(); err != nil {
			return run, err
		}
		// Stamped first, so that one that always fails can't stop the others being reached.
		if err := s.store.MarkFileChecked(ctx, item.userID, item.ID); err != nil {
			return run, err
		}
		read += item.Size
		if err := s.checkFile(ctx, item, &run); err != nil {
			run.Failures = append(run.Failures, fmt.Errorf("file %s: %w", item.ID, err))
		}
	}
	return run, nil
}

func hashObject(ctx context.Context, stored provider.Provider, objectID string) (string, error) {
	reader, err := stored.Get(ctx, objectID, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = reader.Close() }()
	hash := md5.New() //nolint:gosec // Drive's own checksum, which is what a check compares.
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (s *Service) checkFile(ctx context.Context, item checkItem, run *CheckRun) error {
	stored, err := s.providers.Provider(ctx, item.userID, item.ConnectionID)
	if err != nil {
		return err
	}
	sum, err := hashObject(ctx, stored, item.objectID)
	if errors.Is(err, provider.ErrObjectNotFound) {
		// A sync confirms this properly, and removes the record.
		return nil
	}
	if err != nil {
		return err
	}
	if sum == item.checksum {
		run.Checked++
		return s.store.ClearFileProblem(ctx, item.userID, item.ID, ProblemDamaged)
	}

	// Different bytes with a different checksum is a change, which is a sync's to report.
	object, err := stored.Describe(ctx, item.objectID)
	if err != nil {
		return err
	}
	if object.Checksum != item.checksum {
		return nil
	}
	// A move in the meantime leaves this object behind, and that says nothing about the file.
	latest, err := s.store.FileForOwner(ctx, item.userID, item.ID)
	if errors.Is(err, ErrFileNotFound) || (err == nil && latest.objectID != item.objectID) {
		return nil
	}
	if err != nil {
		return err
	}
	run.Damaged++
	return s.store.RecordFileProblem(ctx, item.userID, FileProblem{
		FileID:       &item.ID,
		ConnectionID: item.ConnectionID,
		Name:         item.Name,
		Size:         item.Size,
		Kind:         ProblemDamaged,
	}, "")
}

// FileChecks says how the checks are going for a user, and what they've found.
func (s *Service) FileChecks(ctx context.Context, userID uuid.UUID) (FileChecks, error) {
	checks, err := s.store.CountFileChecks(ctx, userID)
	if err != nil {
		return FileChecks{}, err
	}
	if checks.Enabled, err = s.store.Settings().FileChecksEnabled(ctx); err != nil {
		return FileChecks{}, err
	}
	checks.Problems, err = s.store.FileProblems(ctx, userID)
	return checks, err
}

// DismissProblem takes a problem off the list. A file that changed is accepted as it is
// now, or the next sync would report it again.
func (s *Service) DismissProblem(ctx context.Context, userID, id uuid.UUID) error {
	return s.store.InTx(ctx, func(tx *Store) error {
		problem, reported, err := tx.FileProblem(ctx, userID, id)
		if err != nil {
			return err
		}
		if problem.Kind == ProblemChanged && problem.FileID != nil && reported != "" {
			if err := tx.AcceptFileChecksum(ctx, userID, *problem.FileID, reported); err != nil {
				return err
			}
		}
		return tx.DeleteFileProblem(ctx, userID, id)
	})
}

// DuplicateFile is one copy, with the folders it's in.
type DuplicateFile struct {
	File     File
	Location string
}

// DuplicateGroup is files with the same content.
type DuplicateGroup struct {
	Hash  string
	Size  int64
	Files []DuplicateFile
}

// Duplicates is every group, and what keeping one of each would free.
type Duplicates struct {
	Groups           []DuplicateGroup
	ReclaimableBytes int64
}

// location spells out where a folder is. A loop in the tree ends the walk.
func location(folders map[uuid.UUID]Folder, id *uuid.UUID) string {
	var names []string
	seen := map[uuid.UUID]bool{}
	for id != nil && !seen[*id] {
		seen[*id] = true
		folder, ok := folders[*id]
		if !ok {
			break
		}
		names = append(names, folder.Name)
		id = folder.ParentID
	}
	slices.Reverse(names)
	return strings.Join(names, "/")
}

// Duplicates groups a user's live files by hash, the biggest first. The hash comes from
// the accounts, so finding these reads nothing.
func (s *Service) Duplicates(ctx context.Context, userID uuid.UUID) (Duplicates, error) {
	records, err := s.store.DuplicateFiles(ctx, userID)
	if err != nil {
		return Duplicates{}, err
	}
	folders, err := s.store.AllFolders(ctx, userID)
	if err != nil {
		return Duplicates{}, err
	}
	found := Duplicates{Groups: []DuplicateGroup{}}
	for _, record := range records {
		copied := DuplicateFile{File: record.File, Location: location(folders, record.FolderID)}
		last := len(found.Groups) - 1
		if last >= 0 && found.Groups[last].Hash == record.contentHash && found.Groups[last].Size == record.Size {
			found.Groups[last].Files = append(found.Groups[last].Files, copied)
			found.ReclaimableBytes += record.Size
			continue
		}
		found.Groups = append(found.Groups, DuplicateGroup{Hash: record.contentHash, Size: record.Size, Files: []DuplicateFile{copied}})
	}
	// The row limit can leave the last group with one file, which isn't a duplicate.
	found.Groups = slices.DeleteFunc(found.Groups, func(g DuplicateGroup) bool { return len(g.Files) < 2 })
	slices.SortStableFunc(found.Groups, func(a, b DuplicateGroup) int { return cmp.Compare(b.Size, a.Size) })
	return found, nil
}
