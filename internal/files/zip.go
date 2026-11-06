package files

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// maxZipItems bounds what one request can name.
	maxZipItems = 200
	// maxZipFiles bounds a zip, because each file costs about a second at Google.
	maxZipFiles = 2000
	// missingFilesNote says what couldn't be included, inside the zip itself.
	missingFilesNote = "Files dextro could not find.txt"
	defaultZipName   = "dextro.zip"
)

// Errors for zip downloads.
var (
	ErrInvalidZip  = errors.New("invalid zip request")
	ErrZipTooLarge = errors.New("zip has too many files")
)

// zipEntry is one thing in a zip. A nil file means a directory.
type zipEntry struct {
	path string
	file *File
}

// ZipPlan is a zip worked out in full before anything is sent, because after the
// first byte it's too late to answer with an error.
type ZipPlan struct {
	Name    string
	entries []zipEntry
	used    map[string]bool
}

// unique returns a path nothing in the zip has yet. The check ignores case, because
// unzipping on Windows or macOS would otherwise overwrite one file with another.
func (p *ZipPlan) unique(dir, name string) string {
	extension := path.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	candidate := dir + name
	for n := 2; ; n++ {
		key := strings.ToLower(candidate)
		if !p.used[key] {
			p.used[key] = true
			return candidate
		}
		candidate = fmt.Sprintf("%s%s (%d)%s", dir, stem, n, extension)
	}
}

func (p *ZipPlan) add(entry zipEntry) error {
	if len(p.entries) >= maxZipFiles {
		return ErrZipTooLarge
	}
	p.entries = append(p.entries, entry)
	return nil
}

// PlanZip works out what goes in a zip. Everything named must be the user's own and
// not in the trash, and trashed items inside a folder are left out.
func (s *Service) PlanZip(ctx context.Context, userID uuid.UUID, fileIDs, folderIDs []uuid.UUID) (ZipPlan, error) {
	total := len(fileIDs) + len(folderIDs)
	if total == 0 || total > maxZipItems {
		return ZipPlan{}, ErrInvalidZip
	}
	plan := ZipPlan{Name: defaultZipName, used: map[string]bool{}}
	for _, id := range fileIDs {
		record, err := liveFile(ctx, s.store, userID, id)
		if err != nil {
			return ZipPlan{}, err
		}
		if err := plan.add(zipEntry{path: plan.unique("", record.Name), file: &record.File}); err != nil {
			return ZipPlan{}, err
		}
	}
	for _, id := range folderIDs {
		trail, err := requireLiveFolder(ctx, s.store, userID, &id)
		if err != nil {
			return ZipPlan{}, err
		}
		folder := trail[len(trail)-1]
		if total == 1 {
			plan.Name = folder.Name + ".zip"
		}
		if err := s.planFolder(ctx, userID, &plan, "", folder); err != nil {
			return ZipPlan{}, err
		}
	}
	return plan, nil
}

func (s *Service) planFolder(ctx context.Context, userID uuid.UUID, plan *ZipPlan, parent string, folder Folder) error {
	dir := plan.unique(parent, folder.Name) + "/"
	if err := plan.add(zipEntry{path: dir}); err != nil {
		return err
	}
	files, err := s.store.FilesInFolderForOwner(ctx, userID, &folder.ID)
	if err != nil {
		return err
	}
	for i := range files {
		if err := plan.add(zipEntry{path: plan.unique(dir, files[i].Name), file: &files[i]}); err != nil {
			return err
		}
	}
	folders, err := s.store.FoldersInFolderForOwner(ctx, userID, &folder.ID)
	if err != nil {
		return err
	}
	for _, child := range folders {
		if err := s.planFolder(ctx, userID, plan, dir, child); err != nil {
			return err
		}
	}
	return nil
}

// WriteZip streams a zip. Files are stored rather than compressed: most of what
// people keep is compressed already, and this way a zip costs no real work to make.
// A file that's gone from its account is noted in the zip. Any other failure is
// returned, and the caller has to make sure it doesn't look like a finished zip.
func (s *Service) WriteZip(ctx context.Context, userID uuid.UUID, plan ZipPlan, w io.Writer) error {
	out := zip.NewWriter(w)
	var missing []string
	for _, entry := range plan.entries {
		if entry.file == nil {
			if _, err := out.CreateHeader(&zip.FileHeader{Name: entry.path, Method: zip.Store, Modified: time.Now()}); err != nil {
				return fmt.Errorf("add folder to zip: %w", err)
			}
			continue
		}
		err := s.zipFile(ctx, userID, out, entry)
		if errors.Is(err, ErrObjectMissing) {
			missing = append(missing, entry.path)
			continue
		}
		if err != nil {
			return err
		}
	}
	if len(missing) > 0 {
		part, err := out.CreateHeader(&zip.FileHeader{Name: missingFilesNote, Method: zip.Store, Modified: time.Now()})
		if err != nil {
			return fmt.Errorf("add note to zip: %w", err)
		}
		if _, err := io.WriteString(part, strings.Join(missing, "\n")+"\n"); err != nil {
			return fmt.Errorf("write note: %w", err)
		}
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("finish zip: %w", err)
	}
	return nil
}

func (s *Service) zipFile(ctx context.Context, userID uuid.UUID, out *zip.Writer, entry zipEntry) error {
	reader, err := s.Open(ctx, userID, entry.file.ID, nil)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()
	part, err := out.CreateHeader(&zip.FileHeader{Name: entry.path, Method: zip.Store, Modified: entry.file.UpdatedAt})
	if err != nil {
		return fmt.Errorf("add file to zip: %w", err)
	}
	if _, err := io.Copy(part, reader); err != nil {
		return fmt.Errorf("copy file to zip: %w", err)
	}
	return nil
}
