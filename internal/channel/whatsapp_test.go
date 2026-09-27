package channel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

type fakeCore struct {
	mu   sync.Mutex
	text string
	user string
}

func (f *fakeCore) Handle(_ context.Context, _ string, userID, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.text, f.user = text, userID
	return "ok reply", nil
}

func newTestWhatsApp(core Handler) *whatsapp {
	cfg := config.Channel{
		VerifyToken:   "secret-token",
		PhoneNumberID: "123",
		AccessToken:   "token",
	}
	return NewWhatsApp(cfg, core).(*whatsapp)
}

func TestWebhookVerify(t *testing.T) {
	w := newTestWhatsApp(&fakeCore{})
	req := httptest.NewRequest(http.MethodGet,
		"/webhook/whatsapp?hub.mode=subscribe&hub.verify_token=secret-token&hub.challenge=abc123", nil)
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "abc123" {
		t.Fatalf("good token: got %d %q", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet,
		"/webhook/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=abc123", nil)
	rec = httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad token: got %d, want 403", rec.Code)
	}
}

func TestInboundText(t *testing.T) {
	fc := &fakeCore{}
	w := newTestWhatsApp(fc)
	body := `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"text","text":{"body":"hola"}}]}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fc.mu.Lock()
		got := fc.text
		fc.mu.Unlock()
		if got != "" {
			if got != "hola" || fc.user != "34600123456" {
				t.Fatalf("core got %q from %q", got, fc.user)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("core was never called")
}

func TestDescribeMedia(t *testing.T) {
	var m inboundMessage
	body := `{"from":"1","id":"w","type":"image","image":{"id":"m1","mime_type":"image/jpeg","caption":"my cat"}}`
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if got := describe(m); got != "[image: my cat]" {
		t.Fatalf("image: %q", got)
	}
	body = `{"from":"1","id":"w","type":"location","location":{"latitude":40.4168,"longitude":-3.7038,"name":"Sol","Address":""}}`
	body = strings.Replace(body, `"Address"`, `"address"`, 1)
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if got := describe(m); !strings.Contains(got, "40.4168") || !strings.Contains(got, "Sol") {
		t.Fatalf("location: %q", got)
	}
	body = `{"from":"1","id":"w","type":"text","text":{"body":"replying"},"context":{"id":"wamid.orig"}}`
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if got := quoteOf(m); got != "wamid.orig" {
		t.Fatalf("quote: %q", got)
	}
}

func TestSendText(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("missing auth header")
		}
		got, _ = io.ReadAll(r.Body)
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`{"messages":[{"id":"wamid.sent"}]}`))
	}))
	defer srv.Close()

	w := newTestWhatsApp(&fakeCore{})
	w.baseURL = srv.URL
	if err := w.SendText(context.Background(), "34600123456", "hello", "wamid.orig"); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["to"] != "34600123456" || payload["type"] != "text" {
		t.Fatalf("payload: %v", payload)
	}
	ctx, _ := payload["context"].(map[string]any)
	if ctx["message_id"] != "wamid.orig" {
		t.Fatalf("no quote context: %v", payload)
	}
}

func TestSendTemplate(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		_, _ = rw.Write([]byte(`{}`))
	}))
	defer srv.Close()

	w := newTestWhatsApp(&fakeCore{})
	w.baseURL = srv.URL
	if err := w.SendTemplate(context.Background(), "34600123456", "hello_world", "en_US", []string{"Olin"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"hello_world"`) || !strings.Contains(string(got), `"Olin"`) {
		t.Fatalf("template payload: %s", got)
	}
}

func TestGraphError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusUnauthorized)
		_, _ = rw.Write([]byte(`{"error":{"message":"token expired"}}`))
	}))
	defer srv.Close()

	w := newTestWhatsApp(&fakeCore{})
	w.baseURL = srv.URL
	err := w.SendText(context.Background(), "1", "hi", "")
	if err == nil || !strings.Contains(err.Error(), "token expired") {
		t.Fatalf("expected graph error, got %v", err)
	}
}

func TestSignatureVerification(t *testing.T) {
	secret := "app-secret"
	body := []byte(`{"entry":[]}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	fc := &fakeCore{}
	w := newTestWhatsApp(fc)
	w.cfg.AppSecret = secret

	req := httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", good)
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("good signature: got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	rec = httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: got %d, want 401", rec.Code)
	}
}
