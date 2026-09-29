package google

// Drive client tests (stage 27c): the whole flow against fake
// endpoints - query encoding, auth header on every call, multipart
// upload payload, download bytes, error surfacing.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriveListFiles(t *testing.T) {
	var gotAuth, gotQuery, gotPageSize string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.Query().Get("q")
		gotPageSize = r.URL.Query().Get("pageSize")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"files":[
			{"id":"f1","name":"notes.md","mimeType":"text/markdown","size":"42","modifiedTime":"2026-09-29T07:00:00Z"},
			{"id":"f2","name":"report","mimeType":"application/vnd.google-apps.document"}
		]}`)
	}))
	defer srv.Close()
	d := &Drive{HTTP: srv.Client(), Base: srv.URL}
	d.HTTP = &http.Client{Transport: authRoundTripper{srv.Client().Transport, "Bearer tok"}}
	files, err := d.ListFiles(context.Background(), "name contains 'notes'", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "notes.md" || files[0].Size != 42 {
		t.Fatalf("unexpected files: %+v", files)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("missing auth header: %q", gotAuth)
	}
	if !strings.Contains(gotQuery, "notes") {
		t.Fatalf("query not forwarded: %q", gotQuery)
	}
	if gotPageSize != "20" {
		t.Fatalf("default pageSize: %q", gotPageSize)
	}
}

func TestDriveDownloadFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") != "media" {
			t.Errorf("alt=media missing: %s", r.URL.RawQuery)
		}
		if !strings.HasSuffix(r.URL.Path, "/files/abc123") {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		io.WriteString(w, "file-body-bytes")
	}))
	defer srv.Close()
	d := &Drive{HTTP: srv.Client(), Base: srv.URL}
	raw, err := d.DownloadFile(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "file-body-bytes" {
		t.Fatalf("wrong body: %q", raw)
	}
}

func TestDriveUploadFile(t *testing.T) {
	var gotBody, gotType, gotUploadType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		gotUploadType = r.URL.Query().Get("uploadType")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"new-file-id"}`)
	}))
	defer srv.Close()
	d := &Drive{HTTP: srv.Client(), UploadBase: srv.URL}
	id, err := d.UploadFile(context.Background(), "note.md", "text/markdown", []byte("# hello"), "folder-9")
	if err != nil {
		t.Fatal(err)
	}
	if id != "new-file-id" {
		t.Fatalf("wrong id: %q", id)
	}
	if gotUploadType != "multipart" {
		t.Fatalf("uploadType: %q", gotUploadType)
	}
	if !strings.HasPrefix(gotType, "multipart/related; boundary=") {
		t.Fatalf("content type: %q", gotType)
	}
	for _, want := range []string{`"name":"note.md"`, `"parents":["folder-9"]`, "# hello"} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("multipart body missing %q:\n%s", want, gotBody)
		}
	}
}

func TestDriveErrorsSurface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	d := &Drive{HTTP: srv.Client(), Base: srv.URL}
	if _, err := d.ListFiles(context.Background(), "", 5); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("list should surface the status: %v", err)
	}
	if _, err := d.DownloadFile(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("download should surface the status: %v", err)
	}
	d.UploadBase = srv.URL
	if _, err := d.UploadFile(context.Background(), "n", "", []byte("x"), ""); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("upload should surface the status: %v", err)
	}
}

// authRoundTripper adds a fixed Authorization header, like the OAuth
// transport does for real.
type authRoundTripper struct {
	base http.RoundTripper
	auth string
}

func (a authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", a.auth)
	return a.base.RoundTrip(r)
}
