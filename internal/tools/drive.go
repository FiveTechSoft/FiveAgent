package tools

// Drive tools (stage 27c): same shape as the Gmail and Calendar
// tools - an authorized client factory that answers an honest
// "not connected" error, read (list, download) and write (upload).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/google"
)

// DriveFor builds an authorized Drive client or explains why it
// cannot.
type DriveFor func(ctx context.Context) (*google.Drive, error)

// DriveList lists the files the agent can see.
type DriveList struct {
	Client DriveFor
}

func (t DriveList) Name() string { return "drive_list" }

func (t DriveList) Description() string {
	return "List the user's Drive files the agent can see (its own app scope: files it created). Optional Drive query syntax, e.g. name contains 'report'."
}

func (t DriveList) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "Drive query, e.g. name contains 'notes' - empty lists all visible files"},
			"max_results": {"type": "integer", "description": "1-100, default 20"}
		}
	}`)
}

func (t DriveList) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	files, err := c.ListFiles(ctx, a.Query, a.MaxResults)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "no files visible to the agent", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d file(s):\n", len(files))
	for _, f := range files {
		fmt.Fprintf(&b, "- id:%s | %s (%s)\n", f.ID, f.Name, f.MimeType)
	}
	return b.String(), nil
}

// DriveDownload reads one file's content.
type DriveDownload struct {
	Client DriveFor
}

func (t DriveDownload) Name() string { return "drive_download" }

func (t DriveDownload) Description() string {
	return "Download the content of a Drive file by id (text content up to 4 MiB). Use drive_list first to find the id."
}

func (t DriveDownload) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"file_id": {"type": "string"}
		},
		"required": ["file_id"]
	}`)
}

func (t DriveDownload) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	raw, err := c.DownloadFile(ctx, a.FileID)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// DriveUpload creates one file.
type DriveUpload struct {
	Client DriveFor
}

func (t DriveUpload) Name() string { return "drive_upload" }

func (t DriveUpload) Description() string {
	return "Create a Drive file (name, text content, optional mime type and parent folder id). Confirm the name and content with the user before calling unless they already approved them."
}

func (t DriveUpload) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"content": {"type": "string", "description": "text content of the file"},
			"mime_type": {"type": "string", "description": "default text/plain"},
			"parent_folder": {"type": "string", "description": "optional Drive folder id"}
		},
		"required": ["name", "content"]
	}`)
}

func (t DriveUpload) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Name         string `json:"name"`
		Content      string `json:"content"`
		MimeType     string `json:"mime_type"`
		ParentFolder string `json:"parent_folder"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Name) == "" {
		return "", fmt.Errorf("drive_upload: name is required")
	}
	c, err := t.Client(ctx)
	if err != nil {
		return "", err
	}
	id, err := c.UploadFile(ctx, a.Name, a.MimeType, []byte(a.Content), a.ParentFolder)
	if err != nil {
		return "", fmt.Errorf("drive_upload: %w", err)
	}
	return fmt.Sprintf("file %q created (id %s)", a.Name, id), nil
}
