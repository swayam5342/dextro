package httpx

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spa serves the built frontend. Paths that match a file get that file; any
// other page path gets index.html so the client-side router can handle it.
type spa struct {
	files      fs.FS
	fileServer http.Handler
}

func newSPA(files fs.FS) *spa {
	return &spa{files: files, fileServer: http.FileServerFS(files)}
}

func (s *spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	urlPath := path.Clean("/" + r.URL.Path)

	// API clients and stale asset links need a real 404, never the HTML page.
	if urlPath == "/api" || strings.HasPrefix(urlPath, "/api/") {
		WriteError(w, r, http.StatusNotFound, CodeNotFound, "Not found.")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		WriteError(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method not allowed.")
		return
	}

	name := strings.TrimPrefix(urlPath, "/")
	if name != "" && name != "index.html" && !hasHiddenSegment(name) && s.isFile(name) {
		if strings.HasPrefix(name, "assets/") {
			// Vite puts a content hash in these filenames.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		s.fileServer.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(name, "assets/") {
		WriteError(w, r, http.StatusNotFound, CodeNotFound, "Not found.")
		return
	}
	s.serveIndex(w, r)
}

func (s *spa) isFile(name string) bool {
	info, err := fs.Stat(s.files, name)
	return err == nil && !info.IsDir()
}

func hasHiddenSegment(name string) bool {
	for segment := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(segment, ".") {
			return true
		}
	}
	return false
}

func (s *spa) serveIndex(w http.ResponseWriter, r *http.Request) {
	index, err := fs.ReadFile(s.files, "index.html")
	if err != nil {
		WriteError(w, r, http.StatusServiceUnavailable, CodeUIUnavailable, "The web interface is not available in this build.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index) //nolint:gosec // index.html is embedded at build time, not user input.
}
