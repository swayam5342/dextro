// Package gdrive stores files in a user's Google Drive.
package gdrive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/swayam5342/dextro/internal/provider"
)

const (
	folderName     = "dextro"
	folderMimeType = "application/vnd.google-apps.folder"
	// chunkSize sets the memory an upload uses, since one chunk is held at a time.
	chunkSize       = 8 << 20
	defaultPageSize = 1000
	tokenTimeout    = 30 * time.Second
	fileFields      = "id,name,size,mimeType,modifiedTime,parents"
	// thumbnailSize is the long edge to ask Google for, sharp at the size of a grid tile.
	thumbnailSize = "=s480"
)

// ErrFolderMissing means the user trashed or deleted dextro's folder.
var ErrFolderMissing = errors.New("dextro's drive folder is missing")

// Settings are the non-secret details of a connection.
type Settings struct {
	Email    string `json:"email"`
	FolderID string `json:"folder_id"`
}

// Credentials are the secret details of a connection. Access tokens are short
// lived and fetched as needed, so only the refresh token is kept.
type Credentials struct {
	RefreshToken string `json:"refresh_token"`
}

// Client connects Google accounts. It holds dextro's OAuth client, not any user's
// credentials, so one is shared by everyone.
type Client struct {
	config     oauth2.Config
	apiBase    string
	httpClient *http.Client

	mu sync.Mutex
	// tokens holds one source per refresh token, in memory only. Every request opens
	// its account afresh, and a source keeps its access token until it expires.
	tokens map[string]oauth2.TokenSource
}

// NewClient returns a Client for Google's real endpoints.
func NewClient(clientID, clientSecret, redirectURL string) *Client {
	return newClient(clientID, clientSecret, redirectURL, google.Endpoint.AuthURL, google.Endpoint.TokenURL, "")
}

func newClient(clientID, clientSecret, redirectURL, authURL, tokenURL, apiBase string) *Client {
	return &Client{
		config: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL},
			// drive.file reaches only files dextro created, never the rest of the
			// account. Widening it would hand dextro the user's whole Drive.
			Scopes: []string{drive.DriveFileScope},
		},
		apiBase:    apiBase,
		httpClient: &http.Client{Timeout: tokenTimeout},
		tokens:     map[string]oauth2.TokenSource{},
	}
}

// tokenContext is what tokens are fetched with. It outlives any one request, because
// a token source does, and a download can continue after the call that started it.
func (c *Client) tokenContext() context.Context {
	return context.WithValue(context.Background(), oauth2.HTTPClient, c.httpClient)
}

// tokenSource returns the account's source, so that requests share an access token.
func (c *Client) tokenSource(refreshToken string) oauth2.TokenSource {
	c.mu.Lock()
	defer c.mu.Unlock()
	source, ok := c.tokens[refreshToken]
	if !ok {
		source = c.config.TokenSource(c.tokenContext(), &oauth2.Token{RefreshToken: refreshToken})
		c.tokens[refreshToken] = source
	}
	return source
}

// AuthCodeURL returns the address to send the browser to.
func (c *Client) AuthCodeURL(state, verifier string) string {
	return c.config.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		// select_account lets someone link a second account instead of Google reusing
		// the one they're signed in to. consent is needed because otherwise Google
		// sends a refresh token only the first time someone agrees.
		oauth2.SetAuthURLParam("prompt", "select_account consent"),
		oauth2.S256ChallengeOption(verifier),
	)
}

// Connect finishes the OAuth flow and prepares dextro's folder in the account.
func (c *Client) Connect(ctx context.Context, code, verifier string) (Settings, Credentials, error) {
	exchangeCtx := context.WithValue(ctx, oauth2.HTTPClient, c.httpClient)
	token, err := c.config.Exchange(exchangeCtx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Settings{}, Credentials{}, fmt.Errorf("redeem google code: %w", err)
	}
	if token.RefreshToken == "" {
		return Settings{}, Credentials{}, errors.New("google sent no refresh token")
	}
	service, _, err := c.service(c.config.TokenSource(c.tokenContext(), token))
	if err != nil {
		return Settings{}, Credentials{}, err
	}
	about, err := service.About.Get().Fields("user(emailAddress)").Context(ctx).Do()
	if err != nil {
		return Settings{}, Credentials{}, fmt.Errorf("read drive account: %w", err)
	}
	folderID, err := ensureFolder(ctx, service)
	if err != nil {
		return Settings{}, Credentials{}, err
	}
	return Settings{Email: about.User.EmailAddress, FolderID: folderID}, Credentials{RefreshToken: token.RefreshToken}, nil
}

// ensureFolder finds dextro's folder or creates it. Finding one matters because
// drive.file can see folders from an earlier connection of the same account.
func ensureFolder(ctx context.Context, service *drive.Service) (string, error) {
	query := fmt.Sprintf("mimeType = '%s' and name = '%s' and 'root' in parents and trashed = false", folderMimeType, folderName)
	found, err := service.Files.List().Q(query).Fields("files(id)").PageSize(1).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("find drive folder: %w", err)
	}
	if len(found.Files) > 0 {
		return found.Files[0].Id, nil
	}
	created, err := service.Files.Create(&drive.File{Name: folderName, MimeType: folderMimeType}).
		Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create drive folder: %w", err)
	}
	return created.Id, nil
}

// service builds an API client.
func (c *Client) service(source oauth2.TokenSource) (*drive.Service, *http.Client, error) {
	longLived := c.tokenContext()
	// oauth2 takes only the transport from httpClient, so the timeout applies
	// to fetching tokens and not to transfers, which can take hours.
	httpClient := oauth2.NewClient(longLived, source)
	options := []option.ClientOption{option.WithHTTPClient(httpClient)}
	if c.apiBase != "" {
		options = append(options, option.WithEndpoint(c.apiBase))
	}
	service, err := drive.NewService(longLived, options...)
	if err != nil {
		return nil, nil, fmt.Errorf("create drive client: %w", err)
	}
	return service, httpClient, nil
}

// Open returns a Provider for one connected account.
func (c *Client) Open(_ context.Context, s Settings, cr Credentials) (*Provider, error) {
	source := c.tokenSource(cr.RefreshToken)
	service, httpClient, err := c.service(source)
	if err != nil {
		return nil, err
	}
	return &Provider{service: service, httpClient: httpClient, source: source, folderID: s.FolderID, pageSize: defaultPageSize}, nil
}

// Provider is dextro's folder in one Drive account. It never touches a file's
// permissions, so everything stays private to the account's owner.
type Provider struct {
	service    *drive.Service
	httpClient *http.Client
	source     oauth2.TokenSource
	folderID   string
	pageSize   int64
}

var _ provider.Provider = (*Provider)(nil)

// Check implements provider.Provider.
func (p *Provider) Check(ctx context.Context) error {
	folder, err := p.service.Files.Get(p.folderID).Fields("id,trashed").Context(ctx).Do()
	if isNotFound(err) {
		return ErrFolderMissing
	}
	if err != nil {
		return fmt.Errorf("reach drive: %w", err)
	}
	if folder.Trashed {
		return ErrFolderMissing
	}
	return nil
}

// Quota implements provider.Provider.
func (p *Provider) Quota(ctx context.Context) (provider.Quota, error) {
	about, err := p.service.About.Get().Fields("storageQuota(limit,usage)").Context(ctx).Do()
	if err != nil {
		return provider.Quota{}, fmt.Errorf("read drive quota: %w", err)
	}
	if about.StorageQuota == nil {
		return provider.Quota{}, errors.New("google sent no quota")
	}
	return provider.Quota{Limit: about.StorageQuota.Limit, Used: about.StorageQuota.Usage}, nil
}

// Put implements provider.Provider.
func (p *Provider) Put(ctx context.Context, upload provider.Upload) (provider.Object, error) {
	options := []googleapi.MediaOption{googleapi.ChunkSize(chunkSize)}
	if upload.ContentType != "" {
		options = append(options, googleapi.ContentType(upload.ContentType))
	}
	file := &drive.File{Name: upload.Name, Parents: []string{p.parent(upload.Parent)}}
	created, err := p.service.Files.Create(file).Media(upload.Body, options...).Fields(fileFields).Context(ctx).Do()
	if err != nil {
		return provider.Object{}, fmt.Errorf("upload file: %w", err)
	}
	return toObject(created), nil
}

// Get implements provider.Provider.
func (p *Provider) Get(ctx context.Context, id string, rng *provider.ByteRange) (io.ReadCloser, error) {
	call := p.service.Files.Get(id).Context(ctx)
	if rng != nil {
		call.Header().Set("Range", fmt.Sprintf("bytes=%d-%d", rng.Start, rng.End))
	}
	response, err := call.Download() //nolint:bodyclose // The caller closes it.
	if isNotFound(err) {
		return nil, provider.ErrObjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	return response.Body, nil
}

// Thumbnail implements provider.Provider. Google makes these itself, sometimes a
// while after an upload, and each link is short-lived, so it is fetched every time.
func (p *Provider) Thumbnail(ctx context.Context, id string) (provider.Thumbnail, error) {
	file, err := p.service.Files.Get(id).Fields("thumbnailLink").Context(ctx).Do()
	if isNotFound(err) {
		return provider.Thumbnail{}, provider.ErrObjectNotFound
	}
	if err != nil {
		return provider.Thumbnail{}, fmt.Errorf("find thumbnail: %w", err)
	}
	if file.ThumbnailLink == "" {
		return provider.Thumbnail{}, provider.ErrNoThumbnail
	}
	link := file.ThumbnailLink
	if i := strings.LastIndex(link, "=s"); i >= 0 {
		link = link[:i] + thumbnailSize
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return provider.Thumbnail{}, fmt.Errorf("build thumbnail request: %w", err)
	}
	response, err := p.httpClient.Do(request) //nolint:gosec // The link comes from Google, not from a user.
	if err != nil {
		return provider.Thumbnail{}, fmt.Errorf("fetch thumbnail: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return provider.Thumbnail{}, provider.ErrNoThumbnail
	}
	return provider.Thumbnail{ContentType: response.Header.Get("Content-Type"), Body: response.Body}, nil
}

// Delete implements provider.Provider.
func (p *Provider) Delete(ctx context.Context, id string) error {
	err := p.service.Files.Delete(id).Context(ctx).Do()
	if isNotFound(err) {
		return provider.ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// Describe implements provider.Provider.
func (p *Provider) Describe(ctx context.Context, id string) (provider.Object, error) {
	file, err := p.service.Files.Get(id).Fields(fileFields + ",trashed").Context(ctx).Do()
	if isNotFound(err) {
		return provider.Object{}, provider.ErrObjectNotFound
	}
	if err != nil {
		return provider.Object{}, fmt.Errorf("find file: %w", err)
	}
	if file.Trashed {
		return provider.Object{}, provider.ErrObjectNotFound
	}
	return toObject(file), nil
}

// AccessToken implements provider.Provider.
func (p *Provider) AccessToken(context.Context) (string, error) {
	token, err := p.source.Token()
	if err != nil {
		return "", fmt.Errorf("get access token: %w", err)
	}
	return token.AccessToken, nil
}

// parent turns "dextro's own folder" into its ID.
func (p *Provider) parent(id string) string {
	if id == "" {
		return p.folderID
	}
	return id
}

// RootID implements provider.Provider.
func (p *Provider) RootID() string {
	return p.folderID
}

// CreateFolder implements provider.Provider.
func (p *Provider) CreateFolder(ctx context.Context, parent, name string) (string, error) {
	file := &drive.File{Name: name, MimeType: folderMimeType, Parents: []string{p.parent(parent)}}
	created, err := p.service.Files.Create(file).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create folder: %w", err)
	}
	return created.Id, nil
}

// Rename implements provider.Provider.
func (p *Provider) Rename(ctx context.Context, id, name string) error {
	_, err := p.service.Files.Update(id, &drive.File{Name: name}).Fields("id").Context(ctx).Do()
	if isNotFound(err) {
		return provider.ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Move implements provider.Provider.
func (p *Provider) Move(ctx context.Context, id, parent string) error {
	file, err := p.service.Files.Get(id).Fields("parents").Context(ctx).Do()
	if isNotFound(err) {
		return provider.ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("find parents: %w", err)
	}
	_, err = p.service.Files.Update(id, &drive.File{}).
		AddParents(p.parent(parent)).RemoveParents(strings.Join(file.Parents, ",")).
		Fields("id").Context(ctx).Do()
	if isNotFound(err) {
		return provider.ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("move: %w", err)
	}
	return nil
}

// Trash implements provider.Provider.
func (p *Provider) Trash(ctx context.Context, id string) error {
	_, err := p.service.Files.Update(id, &drive.File{Trashed: true}).Fields("id").Context(ctx).Do()
	if isNotFound(err) {
		return provider.ErrObjectNotFound
	}
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	return nil
}

// Exists implements provider.Provider.
func (p *Provider) Exists(ctx context.Context, id string) (bool, error) {
	file, err := p.service.Files.Get(id).Fields("id,trashed").Context(ctx).Do()
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find file: %w", err)
	}
	return !file.Trashed, nil
}

// List implements provider.Provider.
func (p *Provider) List(ctx context.Context, pageToken string) (provider.ObjectPage, error) {
	// drive.file means this is only what dextro created or was given.
	call := p.service.Files.List().Q("trashed = false").
		Fields(googleapi.Field("nextPageToken,files(" + fileFields + ")")).
		PageSize(p.pageSize).OrderBy("createdTime").Context(ctx)
	if pageToken != "" {
		call = call.PageToken(pageToken)
	}
	found, err := call.Do()
	if err != nil {
		return provider.ObjectPage{}, fmt.Errorf("list files: %w", err)
	}
	page := provider.ObjectPage{Objects: make([]provider.Object, 0, len(found.Files)), NextPageToken: found.NextPageToken}
	for _, file := range found.Files {
		if file.Id != p.folderID {
			page.Objects = append(page.Objects, toObject(file))
		}
	}
	return page, nil
}

func toObject(file *drive.File) provider.Object {
	// A bad timestamp isn't worth failing over; the zero time just shows as unknown.
	modified, _ := time.Parse(time.RFC3339, file.ModifiedTime)
	object := provider.Object{
		ID:          file.Id,
		Name:        file.Name,
		Size:        file.Size,
		ContentType: file.MimeType,
		Folder:      file.MimeType == folderMimeType,
		ModifiedAt:  modified,
	}
	if len(file.Parents) > 0 {
		object.Parent = file.Parents[0]
	}
	return object
}

func isNotFound(err error) bool {
	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound
}
