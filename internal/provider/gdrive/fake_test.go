package gdrive

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const folderMime = "application/vnd.google-apps.folder"

// fakeDrive is the slice of Google's token and Drive APIs the adapter uses.
type fakeDrive struct {
	t      *testing.T
	server *httptest.Server

	mu              sync.Mutex
	files           map[string]*fakeFile
	order           []string
	nextID          int
	uploads         map[string]*fakeFile
	wantCode        string
	gotVerifier     string
	omitRefresh     bool
	refreshRevoked  bool
	permissionCalls int
	// limit and used are what the account reports. A limit of 0 means none.
	limit int64
	used  int64
	// discardBodies throws uploaded bytes away, so that the server does not count
	// towards the memory a test is measuring.
	discardBodies bool
	// thumbnails holds the image Google has made for a file, by file ID.
	thumbnails   map[string][]byte
	gotThumbPath string
}

type fakeFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	Parents      []string `json:"parents"`
	Size         string   `json:"size,omitempty"`
	ModifiedTime string   `json:"modifiedTime"`
	Trashed      bool     `json:"trashed"`
	body         []byte
	discarded    int64
}

func newFakeDrive(t *testing.T) *fakeDrive {
	t.Helper()
	f := &fakeDrive{
		t:          t,
		files:      map[string]*fakeFile{},
		uploads:    map[string]*fakeFile{},
		wantCode:   "the-code",
		thumbnails: map[string][]byte{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", f.token)
	mux.HandleFunc("GET /drive/v3/about", f.authed(f.about))
	mux.HandleFunc("GET /drive/v3/files", f.authed(f.list))
	mux.HandleFunc("POST /drive/v3/files", f.authed(f.create))
	mux.HandleFunc("GET /drive/v3/files/{id}", f.authed(f.get))
	mux.HandleFunc("DELETE /drive/v3/files/{id}", f.authed(f.delete))
	mux.HandleFunc("/drive/v3/files/{id}/permissions", f.permissions)
	mux.HandleFunc("POST /upload/drive/v3/files", f.authed(f.upload))
	mux.HandleFunc("GET /thumb/{id}", f.authed(f.thumbnail))
	mux.HandleFunc("/upload/session/{id}", f.authed(f.chunk))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		http.NotFound(w, r)
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeDrive) token(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.PostFormValue("grant_type") {
	case "authorization_code":
		if r.PostFormValue("code") != f.wantCode {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		f.gotVerifier = r.PostFormValue("code_verifier")
		body := map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600}
		if !f.omitRefresh {
			body["refresh_token"] = "the-refresh-token"
		}
		writeJSON(w, http.StatusOK, body)
	case "refresh_token":
		if f.refreshRevoked || r.PostFormValue("refresh_token") != "the-refresh-token" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (f *fakeDrive) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]any{"code": 401, "message": "no token"}})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		next(w, r)
	}
}

func (f *fakeDrive) about(w http.ResponseWriter, _ *http.Request) {
	quota := map[string]string{"usage": strconv.FormatInt(f.used, 10)}
	if f.limit > 0 {
		quota["limit"] = strconv.FormatInt(f.limit, 10)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user":         map[string]string{"emailAddress": "person@example.org"},
		"storageQuota": quota,
	})
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": 404, "message": "File not found"}})
}

// matches understands the only query shapes the adapter sends.
func matches(file *fakeFile, q string) bool {
	if file.Trashed {
		return false
	}
	isFolder := file.MimeType == folderMime
	if strings.Contains(q, "mimeType = '"+folderMime+"'") {
		return isFolder && strings.Contains(q, "name = '"+file.Name+"'")
	}
	if strings.Contains(q, "mimeType != '"+folderMime+"'") && isFolder {
		return false
	}
	parent, _, _ := strings.Cut(strings.TrimPrefix(q, "'"), "'")
	return slices.Contains(file.Parents, parent)
}

func (f *fakeDrive) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var found []*fakeFile
	for _, id := range f.order {
		if file, ok := f.files[id]; ok && matches(file, q) {
			found = append(found, file)
		}
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("pageToken"))
	size, err := strconv.Atoi(r.URL.Query().Get("pageSize"))
	if err != nil {
		size = 100
	}
	end := min(start+size, len(found))
	body := map[string]any{"files": found[min(start, len(found)):end]}
	if end < len(found) {
		body["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, http.StatusOK, body)
}

func (f *fakeDrive) store(file *fakeFile, body []byte) *fakeFile {
	f.nextID++
	file.ID = "file-" + strconv.Itoa(f.nextID)
	file.ModifiedTime = time.Now().UTC().Format(time.RFC3339)
	file.body = body
	if file.MimeType != folderMime {
		file.Size = strconv.FormatInt(int64(len(body))+file.discarded, 10)
	}
	f.files[file.ID] = file
	f.order = append(f.order, file.ID)
	return file
}

func (f *fakeDrive) create(w http.ResponseWriter, r *http.Request) {
	var file fakeFile
	if err := json.NewDecoder(r.Body).Decode(&file); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, f.store(&file, nil))
}

func (f *fakeDrive) get(w http.ResponseWriter, r *http.Request) {
	file, ok := f.files[r.PathValue("id")]
	if !ok {
		notFound(w)
		return
	}
	if r.URL.Query().Get("fields") == "thumbnailLink" {
		body := map[string]string{}
		if f.thumbnails[file.ID] != nil {
			body["thumbnailLink"] = f.server.URL + "/thumb/" + file.ID + "=s220"
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	if r.URL.Query().Get("alt") == "media" {
		body := file.body
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err == nil {
			body = body[start : end+1]
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(body)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func (f *fakeDrive) thumbnail(w http.ResponseWriter, r *http.Request) {
	f.gotThumbPath = r.URL.Path
	id, _, _ := strings.Cut(r.PathValue("id"), "=")
	body, ok := f.thumbnails[id]
	if !ok {
		notFound(w)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	_, _ = w.Write(body)
}

func (f *fakeDrive) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := f.files[id]; !ok {
		notFound(w)
		return
	}
	delete(f.files, id)
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeDrive) permissions(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permissionCalls++
	f.t.Error("the adapter touched a file's permissions")
	w.WriteHeader(http.StatusForbidden)
}

func (f *fakeDrive) upload(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("uploadType") {
	case "multipart":
		f.uploadMultipart(w, r)
	case "resumable":
		var file fakeFile
		if err := json.NewDecoder(r.Body).Decode(&file); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session := fmt.Sprintf("s%d", len(f.uploads)+1)
		f.uploads[session] = &file
		w.Header().Set("Location", f.server.URL+"/upload/session/"+session)
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "unknown upload type", http.StatusBadRequest)
	}
}

func (f *fakeDrive) uploadMultipart(w http.ResponseWriter, r *http.Request) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	metadata, err := reader.NextPart()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var file fakeFile
	if err := json.NewDecoder(metadata).Decode(&file); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	media, err := reader.NextPart()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(media)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, f.store(&file, body))
}

// chunk takes one piece of a resumable upload. The last chunk states the total.
func (f *fakeDrive) chunk(w http.ResponseWriter, r *http.Request) {
	session := r.PathValue("id")
	file, ok := f.uploads[session]
	if !ok {
		notFound(w)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if f.discardBodies {
		file.discarded += int64(len(body))
	} else {
		file.body = append(file.body, body...)
	}
	total := r.Header.Get("Content-Range")[strings.LastIndexByte(r.Header.Get("Content-Range"), '/')+1:]
	if total == "*" {
		// Google answers this way when the client sends X-GUploader-No-308.
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", int64(len(file.body))+file.discarded-1))
		w.Header().Set("X-Http-Status-Code-Override", "308")
		w.WriteHeader(http.StatusOK)
		return
	}
	delete(f.uploads, session)
	writeJSON(w, http.StatusOK, f.store(file, file.body))
}

func (f *fakeDrive) countFolders() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, file := range f.files {
		if file.MimeType == folderMime {
			count++
		}
	}
	return count
}
