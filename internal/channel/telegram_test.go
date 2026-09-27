package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTelegram is a stub Bot API server that records calls.
type fakeTelegram struct {
	mu    sync.Mutex
	calls map[string][]map[string]any // method -> payloads
}

func newFakeTelegram(t *testing.T) (*fakeTelegram, *httptest.Server) {
	t.Helper()
	f := &fakeTelegram{calls: map[string][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var payload map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &payload)
		f.mu.Lock()
		f.calls[method] = append(f.calls[method], payload)
		f.mu.Unlock()

		var result any
		switch method {
		case "getMe":
			result = map[string]any{"id": 1, "username": "fiveagent_test_bot"}
		case "getUpdates":
			result = []map[string]any{{
				"update_id": 100,
				"message": map[string]any{
					"message_id": 7,
					"from":       map[string]any{"id": 42, "first_name": "Olin"},
					"chat":       map[string]any{"id": 42},
					"text":       "hola bot",
				},
			}}
		default:
			result = map[string]any{"message_id": 8}
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeTelegram) got(method string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func TestTelegramFlow(t *testing.T) {
	f, srv := newFakeTelegram(t)
	fc := &fakeCore{}
	tg := NewTelegram("token", fc).(*telegram)
	tg.baseURL = srv.URL
	tg.http = &http.Client{Timeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tg.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fc.mu.Lock()
		got := fc.text
		fc.mu.Unlock()
		msgs := f.got("sendMessage")
		if got != "" && len(msgs) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	fc.mu.Lock()
	if fc.text != "hola bot" || fc.user != "42" {
		t.Fatalf("core got %q from %q", fc.text, fc.user)
	}
	fc.mu.Unlock()

	msgs := f.got("sendMessage")
	if len(msgs) == 0 {
		t.Fatal("no sendMessage call")
	}
	m := msgs[0]
	if m["chat_id"] != "42" || m["text"] != "ok reply" {
		t.Fatalf("sendMessage payload: %v", m)
	}
	if m["reply_to_message_id"].(float64) != 7 {
		t.Fatalf("no reply quote: %v", m)
	}
	if len(f.got("sendChatAction")) == 0 {
		t.Fatal("no typing indicator sent")
	}
	if len(f.got("getMe")) == 0 {
		t.Fatal("token never checked with getMe")
	}
}

func TestTelegramDescribe(t *testing.T) {
	cases := []struct {
		json string
		want string
	}{
		{`{"text":"hi"}`, "hi"},
		{`{"photo":[{"file_id":"p1"}],"caption":"my cat"}`, "[photo: my cat]"},
		{`{"voice":{"file_id":"v1","duration":3}}`, "[voice message, 3s]"},
		{`{"document":{"file_id":"d1","file_name":"a.pdf"}}`, "[document: a.pdf]"},
	}
	for _, c := range cases {
		var m tgMessage
		if err := json.Unmarshal([]byte(c.json), &m); err != nil {
			t.Fatal(err)
		}
		if got := m.describe(); got != c.want {
			t.Errorf("%s -> %q, want %q", c.json, got, c.want)
		}
	}
}

func TestTelegramAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(rw).Encode(map[string]any{"ok": false, "description": "Unauthorized"})
	}))
	defer srv.Close()
	tg := NewTelegram("bad-token", &fakeCore{}).(*telegram)
	tg.baseURL = srv.URL
	if err := tg.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("expected Unauthorized, got %v", err)
	}
}
