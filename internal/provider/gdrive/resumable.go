package gdrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"

	"github.com/swayam5342/dextro/internal/provider"
)

const (
	uploadPath = "/upload/drive/v3/files"
	// Google's answers to a resumable upload that aren't standard HTTP.
	statusResumeIncomplete = 308
	statusClientClosed     = 499
)

// plainError removes the request address from a transport error. A session
// address is a secret, and Go puts the URL in the errors its client returns.
func plainError(err error) error {
	var withAddress *url.Error
	if errors.As(err, &withAddress) {
		return withAddress.Err
	}
	return err
}

// BeginResumable implements provider.Provider.
func (p *Provider) BeginResumable(ctx context.Context, upload provider.Resumable) (string, error) {
	metadata, err := json.Marshal(&drive.File{Name: upload.Name, Parents: []string{p.folderID}})
	if err != nil {
		return "", fmt.Errorf("encode file details: %w", err)
	}
	target := googleapi.ResolveRelative(p.service.BasePath, uploadPath) +
		"?uploadType=resumable&fields=" + url.QueryEscape(fileFields)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(metadata))
	if err != nil {
		return "", fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(upload.Size, 10))
	if upload.ContentType != "" {
		req.Header.Set("X-Upload-Content-Type", upload.ContentType)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("start upload: %w", plainError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("start upload: drive answered %d", resp.StatusCode)
	}
	session := resp.Header.Get("Location")
	if session == "" {
		return "", errors.New("start upload: drive sent no session address")
	}
	return session, nil
}

// PutChunk implements provider.Provider.
func (p *Provider) PutChunk(ctx context.Context, session string, chunk provider.Chunk) (provider.ResumeState, error) {
	contentRange := fmt.Sprintf("bytes %d-%d/%d", chunk.Offset, chunk.Offset+chunk.Length-1, chunk.Total)
	return p.send(ctx, session, chunk.Body, chunk.Length, contentRange)
}

// ResumableStatus implements provider.Provider.
func (p *Provider) ResumableStatus(ctx context.Context, session string, total int64) (provider.ResumeState, error) {
	return p.send(ctx, session, http.NoBody, 0, fmt.Sprintf("bytes */%d", total))
}

func (p *Provider) send(ctx context.Context, session string, body io.Reader, length int64, contentRange string) (provider.ResumeState, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, session, body)
	if err != nil {
		return provider.ResumeState{}, errors.New("build chunk request: bad session address")
	}
	req.ContentLength = length
	req.Header.Set("Content-Range", contentRange)
	// A real 308 looks like a redirect, so Google offers to send a 200 that says so.
	req.Header.Set("X-GUploader-No-308", "yes")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return provider.ResumeState{}, fmt.Errorf("send chunk: %w", plainError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	status := resp.StatusCode
	if resp.Header.Get("X-Http-Status-Code-Override") == strconv.Itoa(statusResumeIncomplete) {
		status = statusResumeIncomplete
	}
	switch status {
	case statusResumeIncomplete:
		received, err := parseReceived(resp.Header.Get("Range"))
		if err != nil {
			return provider.ResumeState{}, err
		}
		return provider.ResumeState{Received: received}, nil
	case http.StatusOK, http.StatusCreated:
		var file drive.File
		if err := json.NewDecoder(resp.Body).Decode(&file); err != nil {
			return provider.ResumeState{}, fmt.Errorf("read finished upload: %w", err)
		}
		object := toObject(&file)
		return provider.ResumeState{Received: object.Size, Object: &object}, nil
	case http.StatusNotFound, http.StatusGone:
		return provider.ResumeState{}, provider.ErrSessionExpired
	}
	return provider.ResumeState{}, fmt.Errorf("send chunk: drive answered %d", status)
}

// parseReceived reads Google's "bytes=0-N". No header means it has kept nothing.
func parseReceived(header string) (int64, error) {
	if header == "" {
		return 0, nil
	}
	last, ok := strings.CutPrefix(header, "bytes=0-")
	if !ok {
		return 0, fmt.Errorf("drive sent an odd range: %q", header)
	}
	end, err := strconv.ParseInt(last, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("drive sent an odd range: %q", header)
	}
	return end + 1, nil
}

// CancelResumable implements provider.Provider.
func (p *Provider) CancelResumable(ctx context.Context, session string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, session, http.NoBody)
	if err != nil {
		return errors.New("build cancel request: bad session address")
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cancel upload: %w", plainError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case statusClientClosed, http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusGone:
		return nil
	}
	return fmt.Errorf("cancel upload: drive answered %d", resp.StatusCode)
}
