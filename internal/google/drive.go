package google

// Drive client (stage 27c): same shape as the Gmail and Calendar
// clients - overridable base URLs, OAuth-authorized HTTP client from
// the shared oauth package, read (list, download) and write (upload).
// Scope is drive.file: the agent sees and manages only the files it
// created (or that were opened with it), never the whole Drive.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// DriveBase is the Drive API root, overridable in tests.
var DriveBase = "https://www.googleapis.com/drive/v3"

// DriveUploadBase is the Drive upload root, overridable in tests.
var DriveUploadBase = "https://www.googleapis.com/upload/drive/v3"

// Drive lists, downloads and uploads files.
type Drive struct {
	HTTP       *http.Client
	Base       string // empty uses DriveBase
	UploadBase string // empty uses DriveUploadBase
}

func (d *Drive) base() string {
	if d.Base != "" {
		return d.Base
	}
	return DriveBase
}

func (d *Drive) uploadBase() string {
	if d.UploadBase != "" {
		return d.UploadBase
	}
	return DriveUploadBase
}

// DriveFile is one Drive entry.
type DriveFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size,string,omitempty"`
	Modified string `json:"modifiedTime,omitempty"`
}

// ListFiles returns the files matching query (Drive query syntax; an
// empty query lists everything the app can see).
func (d *Drive) ListFiles(ctx context.Context, query string, maxResults int) ([]DriveFile, error) {
	if maxResults <= 0 || maxResults > 100 {
		maxResults = 20
	}
	u := d.base() + "/files?fields=files(id,name,mimeType,size,modifiedTime)" +
		fmt.Sprintf("&pageSize=%d", maxResults)
	if strings.TrimSpace(query) != "" {
		u += "&q=" + url.QueryEscape(query)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("drive: %s", resp.Status)
	}
	var parsed struct {
		Files []DriveFile `json:"files"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, err
	}
	return parsed.Files, nil
}

// DownloadFile returns the content of one file (capped at 4 MiB; the
// agent deals with notes and documents, not archives).
func (d *Drive) DownloadFile(ctx context.Context, id string) ([]byte, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("drive: file id is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.base()+"/files/"+url.PathEscape(id)+"?alt=media", nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("drive: %s", resp.Status)
	}
	return raw, nil
}

// UploadFile creates a file with metadata and content in one
// multipart request and returns its id.
func (d *Drive) UploadFile(ctx context.Context, name, mimeType string, content []byte, parentFolder string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("drive: file name is required")
	}
	if mimeType == "" {
		mimeType = "text/plain"
	}
	meta := map[string]any{"name": name, "mimeType": mimeType}
	if parentFolder != "" {
		meta["parents"] = []string{parentFolder}
	}
	metaRaw, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	metaHead := textproto.MIMEHeader{}
	metaHead.Set("Content-Type", "application/json; charset=UTF-8")
	metaPart, err := w.CreatePart(metaHead)
	if err != nil {
		return "", err
	}
	if _, err := metaPart.Write(metaRaw); err != nil {
		return "", err
	}
	contentHead := textproto.MIMEHeader{}
	contentHead.Set("Content-Type", mimeType)
	contentPart, err := w.CreatePart(contentHead)
	if err != nil {
		return "", err
	}
	if _, err := contentPart.Write(content); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.uploadBase()+"/files?uploadType=multipart&fields=id", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "multipart/related; boundary="+w.Boundary())
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("drive: %s", resp.Status)
	}
	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", err
	}
	return parsed.ID, nil
}
