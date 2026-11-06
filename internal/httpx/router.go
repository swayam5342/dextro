// Package httpx builds the HTTP router and holds what every handler shares.
package httpx

import (
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5"
	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

// AuthHandlers is the part of the API the auth feature implements.
type AuthHandlers interface {
	GetAuthConfig(w http.ResponseWriter, r *http.Request)
	Register(w http.ResponseWriter, r *http.Request)
	Login(w http.ResponseWriter, r *http.Request)
	Logout(w http.ResponseWriter, r *http.Request)
	GetMe(w http.ResponseWriter, r *http.Request)
	ChangePassword(w http.ResponseWriter, r *http.Request)
	ListSessions(w http.ResponseWriter, r *http.Request)
	RevokeSession(w http.ResponseWriter, r *http.Request, id types.UUID)
	StartGoogleSignIn(w http.ResponseWriter, r *http.Request)
	LinkGoogle(w http.ResponseWriter, r *http.Request)
	GoogleCallback(w http.ResponseWriter, r *http.Request, params apigen.GoogleCallbackParams)
	ListAPIKeys(w http.ResponseWriter, r *http.Request)
	CreateAPIKey(w http.ResponseWriter, r *http.Request)
	RevokeAPIKey(w http.ResponseWriter, r *http.Request, id types.UUID)
	ListUsers(w http.ResponseWriter, r *http.Request)
	CreateUser(w http.ResponseWriter, r *http.Request)
	DeleteUser(w http.ResponseWriter, r *http.Request, id types.UUID)
	ResetUserPassword(w http.ResponseWriter, r *http.Request, id types.UUID)
}

// FilesHandlers is the part of the API the files feature implements.
type FilesHandlers interface {
	ListFolder(w http.ResponseWriter, r *http.Request, params apigen.ListFolderParams)
	UploadFile(w http.ResponseWriter, r *http.Request, params apigen.UploadFileParams)
	GetFile(w http.ResponseWriter, r *http.Request, id types.UUID)
	RenameFile(w http.ResponseWriter, r *http.Request, id types.UUID)
	DeleteFile(w http.ResponseWriter, r *http.Request, id types.UUID)
	DownloadFile(w http.ResponseWriter, r *http.Request, id types.UUID, params apigen.DownloadFileParams)
	GetThumbnail(w http.ResponseWriter, r *http.Request, id types.UUID)
	MoveFile(w http.ResponseWriter, r *http.Request, id types.UUID)
	TrashFile(w http.ResponseWriter, r *http.Request, id types.UUID)
	RestoreFile(w http.ResponseWriter, r *http.Request, id types.UUID)
	CreateFolder(w http.ResponseWriter, r *http.Request)
	RenameFolder(w http.ResponseWriter, r *http.Request, id types.UUID)
	DeleteFolder(w http.ResponseWriter, r *http.Request, id types.UUID)
	MoveFolder(w http.ResponseWriter, r *http.Request, id types.UUID)
	TrashFolder(w http.ResponseWriter, r *http.Request, id types.UUID)
	RestoreFolder(w http.ResponseWriter, r *http.Request, id types.UUID)
	ListTrash(w http.ResponseWriter, r *http.Request)
	EmptyTrash(w http.ResponseWriter, r *http.Request)
	Search(w http.ResponseWriter, r *http.Request, params apigen.SearchParams)
	ListUploads(w http.ResponseWriter, r *http.Request)
	BeginUpload(w http.ResponseWriter, r *http.Request)
	GetUpload(w http.ResponseWriter, r *http.Request, id types.UUID)
	CancelUpload(w http.ResponseWriter, r *http.Request, id types.UUID)
	PutUploadChunk(w http.ResponseWriter, r *http.Request, id types.UUID, params apigen.PutUploadChunkParams)
	SyncConnection(w http.ResponseWriter, r *http.Request, id types.UUID)
	ImportPicked(w http.ResponseWriter, r *http.Request, id types.UUID)
}

// StorageHandlers is the part of the API the storage feature implements.
type StorageHandlers interface {
	ListConnections(w http.ResponseWriter, r *http.Request)
	GetPool(w http.ResponseWriter, r *http.Request)
	DeleteConnection(w http.ResponseWriter, r *http.Request, id types.UUID)
	TestConnection(w http.ResponseWriter, r *http.Request, id types.UUID)
	OpenPicker(w http.ResponseWriter, r *http.Request, id types.UUID)
	ConnectGoogleDrive(w http.ResponseWriter, r *http.Request)
	GoogleDriveCallback(w http.ResponseWriter, r *http.Request, params apigen.GoogleDriveCallbackParams)
}

// AdminHandlers is the part of the API the admin feature implements.
type AdminHandlers interface {
	GetSettings(w http.ResponseWriter, r *http.Request)
	UpdateSettings(w http.ResponseWriter, r *http.Request)
	GetServerStatus(w http.ResponseWriter, r *http.Request)
	ListAuditEvents(w http.ResponseWriter, r *http.Request, params apigen.ListAuditEventsParams)
}

// Deps is everything the router needs.
type Deps struct {
	Logger *slog.Logger
	DB     Pinger
	// PublicOrigin is the only Origin allowed to make changing requests.
	PublicOrigin   string
	TrustedProxies []netip.Prefix
	Sessions       SessionResolver
	APIKeys        APIKeyResolver
	Auth           AuthHandlers
	Admin          AdminHandlers
	Storage        StorageHandlers
	Files          FilesHandlers
}

// NewRouter returns the handler for the whole server.
func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(assignRequestID, accessLog(d.Logger), recoverPanics(d.Logger), securityHeaders, resolveClientIP(d.TrustedProxies))

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusNotFound, CodeNotFound, "Not found.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "Method not allowed.")
	})

	server := &api{
		AuthHandlers:    d.Auth,
		AdminHandlers:   d.Admin,
		StorageHandlers: d.Storage,
		FilesHandlers:   d.Files,
		db:              d.DB,
		logger:          d.Logger,
	}
	apigen.HandlerWithOptions(server, apigen.ChiServerOptions{
		BaseRouter: r,
		// These wrap each generated route, and the last one listed runs first.
		Middlewares: []apigen.MiddlewareFunc{
			requireAdmin,
			requirePasswordChange,
			requireKeyScope,
			requireSession,
			loadSession(d.Sessions, d.APIKeys, d.Logger),
			requireOrigin(d.PublicOrigin),
		},
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			d.Logger.WarnContext(r.Context(), "invalid request",
				"request_id", RequestID(r.Context()), "error", err)
			WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "The request is not valid.")
		},
	})
	return r
}
