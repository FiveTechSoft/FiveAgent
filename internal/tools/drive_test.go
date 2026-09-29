package tools

// Drive tools tests (stage 27c): honest not-connected errors, wiring
// of args into the client, formatted output.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/google"
)

func driveNotConnected(ctx context.Context) (*google.Drive, error) {
	return nil, fmt.Errorf("[drive not connected - ask the owner to open /oauth/drive/start]")
}

func TestDriveToolsHonestWhenNotConnected(t *testing.T) {
	for name, tool := range map[string]Tool{
		"drive_list":     DriveList{Client: driveNotConnected},
		"drive_download": DriveDownload{Client: driveNotConnected},
		"drive_upload":   DriveUpload{Client: driveNotConnected},
	} {
		_, err := tool.Execute(context.Background(), []byte(`{"file_id":"x","name":"n","content":"c"}`))
		if err == nil || !strings.Contains(err.Error(), "not connected") {
			t.Fatalf("%s should surface the honest not-connected error, got: %v", name, err)
		}
	}
}

func TestDriveListFormatsFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"files":[{"id":"f1","name":"notes.md","mimeType":"text/markdown"}]}`)
	}))
	defer srv.Close()
	tool := DriveList{Client: func(ctx context.Context) (*google.Drive, error) {
		return &google.Drive{HTTP: srv.Client(), Base: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), []byte(`{"query":"name contains 'notes'"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "notes.md") || !strings.Contains(out, "id:f1") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestDriveUploadReturnsID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"created-1"}`)
	}))
	defer srv.Close()
	tool := DriveUpload{Client: func(ctx context.Context) (*google.Drive, error) {
		return &google.Drive{HTTP: srv.Client(), UploadBase: srv.URL}, nil
	}}
	out, err := tool.Execute(context.Background(), []byte(`{"name":"note.md","content":"body"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created-1") {
		t.Fatalf("upload should report the new id: %q", out)
	}
}

func TestDriveUploadRequiresName(t *testing.T) {
	tool := DriveUpload{Client: driveNotConnected}
	if _, err := tool.Execute(context.Background(), []byte(`{"content":"x"}`)); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("name validation should fire before connecting: %v", err)
	}
}
