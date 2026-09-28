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

// newTestWhatsApp builds an adapter pointed at a discard-all fake Graph
// API: no test may ever touch graph.facebook.com (network calls made the
// debounce tests flaky and leaked tokens into access logs).
func newTestWhatsApp(t *testing.T, core Handler) *whatsapp {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		rw.Write([]byte(`{"messages":[{"id":"wamid.fake"}]}`))
	}))
	t.Cleanup(srv.Close)
	cfg := config.Channel{
		VerifyToken:   "secret-token",
		PhoneNumberID: "123",
		AccessToken:   "token",
	}
	w := NewWhatsApp(cfg, core).(*whatsapp)
	w.baseURL = srv.URL
	return w
}

func TestWebhookVerify(t *testing.T) {
	w := newTestWhatsApp(t, &fakeCore{})
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
	w := newTestWhatsApp(t, fc)
	w.debounce = 10 * time.Millisecond // webhook path test, not a debounce test
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

	w := newTestWhatsApp(t, &fakeCore{})
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

	w := newTestWhatsApp(t, &fakeCore{})
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

	w := newTestWhatsApp(t, &fakeCore{})
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
	w := newTestWhatsApp(t, fc)
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

func TestWhatsAppAllowedSenders(t *testing.T) {
	fc := &fakeCore{}
	cfg := config.Channel{
		VerifyToken:    "secret-token",
		PhoneNumberID:  "123",
		AccessToken:    "token",
		AllowedSenders: []string{"34600999888"},
	}
	w := NewWhatsApp(cfg, fc).(*whatsapp)
	body := `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"text","text":{"body":"hola"}}]}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	time.Sleep(300 * time.Millisecond)
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.text != "" {
		t.Fatalf("non-listed sender reached the core: %q", fc.text)
	}
}

func TestWhatsAppAllowedSendersListed(t *testing.T) {
	fc := &fakeCore{}
	cfg := config.Channel{
		VerifyToken:    "secret-token",
		PhoneNumberID:  "123",
		AccessToken:    "token",
		AllowedSenders: []string{"34600123456"},
	}
	w := NewWhatsApp(cfg, fc).(*whatsapp)
	w.debounce = 10 * time.Millisecond // webhook path test, not a debounce test
	body := `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.1","type":"text","text":{"body":"hola"}}]}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fc.mu.Lock()
		got := fc.text
		fc.mu.Unlock()
		if got == "hola" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("listed sender never reached the core")
}

// slowCore simulates a model that never answers in time: it blocks
// until the agent context expires and returns the deadline error.
type slowCore struct{}

func (slowCore) Handle(ctx context.Context, _, _, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

// Regression test for the production bug "model timeout -> total
// silence": the send must use a fresh context, and the user must get a
// short timeout message, not nothing.
func TestProcessSendsFallbackWhenModelTimesOut(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, string(b))
		mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		io.WriteString(rw, `{"messages":[{"id":"wamid.x"}]}`)
	}))
	defer srv.Close()

	w := newTestWhatsApp(t, slowCore{})
	w.baseURL = srv.URL
	w.agentTimeout = 50 * time.Millisecond
	w.process("34600111222", "wamid.inbound", "hola", "")

	mu.Lock()
	defer mu.Unlock()
	if len(sent) == 0 {
		t.Fatal("no reply was sent after the model timeout")
	}
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "tardando demasiado") {
		t.Fatalf("expected the timeout fallback message, got: %s", joined)
	}
}

// blockingCore takes a while per message, to prove two senders are
// handled concurrently and neither message is lost.
type blockingCore struct {
	delay time.Duration
	mu    sync.Mutex
	seen  []string
}

func (b *blockingCore) Handle(_ context.Context, _, userID, _ string) (string, error) {
	time.Sleep(b.delay)
	b.mu.Lock()
	b.seen = append(b.seen, userID)
	b.mu.Unlock()
	return "respuesta", nil
}

func TestProcessConcurrentSenders(t *testing.T) {
	var mu sync.Mutex
	replies := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		var body struct {
			To string `json:"to"`
		}
		raw, _ := io.ReadAll(r.Body)
		// mark-read posts carry no "to"; reaction posts carry "to" but
		// are not replies; only text posts count as replies
		var full map[string]any
		json.Unmarshal(raw, &full)
		if to, ok := full["to"].(string); ok && full["type"] == "text" {
			body.To = to
			mu.Lock()
			replies[body.To]++
			mu.Unlock()
		}
		rw.Header().Set("Content-Type", "application/json")
		io.WriteString(rw, `{"messages":[{"id":"wamid.x"}]}`)
	}))
	defer srv.Close()

	core := &blockingCore{delay: 300 * time.Millisecond}
	w := newTestWhatsApp(t, core)
	w.baseURL = srv.URL
	w.agentTimeout = 5 * time.Second

	start := time.Now()
	var wg sync.WaitGroup
	for _, sender := range []string{"34623521270", "34722461100"} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			w.process(s, "wamid."+s, "hola", "")
		}(sender)
	}
	wg.Wait()
	elapsed := time.Since(start)

	mu.Lock()
	defer mu.Unlock()
	for _, s := range []string{"34623521270", "34722461100"} {
		if replies[s] != 1 {
			t.Fatalf("sender %s got %d replies, want 1", s, replies[s])
		}
	}
	if len(core.seen) != 2 {
		t.Fatalf("core saw %d messages, want 2", len(core.seen))
	}
	if elapsed > 600*time.Millisecond {
		t.Fatalf("senders look serialized: %s for two 300ms replies", elapsed)
	}
}

// quickCore answers instantly.
type quickCore struct{}

func (quickCore) Handle(_ context.Context, _, _, _ string) (string, error) {
	return "ahí va", nil
}

// reactionCapture records the reaction payloads in order.
type reactionCapture struct {
	mu    sync.Mutex
	react []string
	ids   []string
	texts int
}

func (c *reactionCapture) handler(rw http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var full map[string]any
	json.Unmarshal(raw, &full)
	c.mu.Lock()
	if full["type"] == "reaction" {
		if rec, ok := full["reaction"].(map[string]any); ok {
			c.react = append(c.react, rec["emoji"].(string))
			c.ids = append(c.ids, rec["message_id"].(string))
		}
	}
	if full["type"] == "text" {
		c.texts++
	}
	c.mu.Unlock()
	rw.Header().Set("Content-Type", "application/json")
	io.WriteString(rw, `{"messages":[{"id":"wamid.x"}]}`)
}

func TestProcessReactions(t *testing.T) {
	cap := &reactionCapture{}
	srv := httptest.NewServer(http.HandlerFunc(cap.handler))
	defer srv.Close()

	w := newTestWhatsApp(t, quickCore{})
	w.baseURL = srv.URL
	w.process("34600111222", "wamid.inbound", "hola", "")

	cap.mu.Lock()
	defer cap.mu.Unlock()
	want := []string{"\U0001F440", "✅"}
	if len(cap.react) != len(want) || cap.react[0] != want[0] || cap.react[1] != want[1] {
		t.Fatalf("reactions = %v, want %v (eyes while working, check when done)", cap.react, want)
	}
	if cap.texts != 1 {
		t.Fatalf("text replies = %d, want 1", cap.texts)
	}
}

func TestProcessReactionOnAgentFailure(t *testing.T) {
	cap := &reactionCapture{}
	srv := httptest.NewServer(http.HandlerFunc(cap.handler))
	defer srv.Close()

	w := newTestWhatsApp(t, slowCore{})
	w.baseURL = srv.URL
	w.agentTimeout = 50 * time.Millisecond
	w.process("34600111222", "wamid.inbound", "hola", "")

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.react) != 2 || cap.react[1] != "⚠️" {
		t.Fatalf("reactions = %v, want [👀 ⚠️] on agent failure", cap.react)
	}
	if cap.texts != 1 {
		t.Fatalf("fallback reply not sent: texts = %d", cap.texts)
	}
}

func TestProcessReactionsOff(t *testing.T) {
	cap := &reactionCapture{}
	srv := httptest.NewServer(http.HandlerFunc(cap.handler))
	defer srv.Close()

	w := newTestWhatsApp(t, quickCore{})
	w.baseURL = srv.URL
	w.reactions = false
	w.process("34600111222", "wamid.inbound", "hola", "")

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.react) != 0 {
		t.Fatalf("reactions disabled but got %v", cap.react)
	}
	if cap.texts != 1 {
		t.Fatalf("text replies = %d, want 1", cap.texts)
	}
}

// turnCapture counts and records every agent turn.
type turnCapture struct {
	mu    sync.Mutex
	texts []string
}

func (c *turnCapture) Handle(_ context.Context, _, _, text string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, text)
	return "ok", nil
}

func (c *turnCapture) turns() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

// TestDebounceBurst: two messages inside the window become ONE agent
// turn with both texts in arrival order; reactions: 👀 on the first
// message, ✅ on every message of the burst; one reply.
func TestDebounceBurst(t *testing.T) {
	cap := &reactionCapture{}
	srv := httptest.NewServer(http.HandlerFunc(cap.handler))
	defer srv.Close()
	core := &turnCapture{}
	w := newTestWhatsApp(t, core)
	w.baseURL = srv.URL
	w.debounce = 100 * time.Millisecond

	w.enqueue("34600111222", "wamid.1", "hola", "")
	time.Sleep(20 * time.Millisecond)
	w.enqueue("34600111222", "wamid.2", "mundo", "wamid.2")
	time.Sleep(400 * time.Millisecond)

	turns := core.turns()
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1 (burst must join): %q", len(turns), turns)
	}
	if turns[0] != "hola\nmundo" {
		t.Fatalf("burst text = %q, want %q (order preserved)", turns[0], "hola\nmundo")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	wantReact := []string{"👀", "✅", "✅"}
	wantIDs := []string{"wamid.1", "wamid.1", "wamid.2"}
	if len(cap.react) != 3 {
		t.Fatalf("reactions = %v %v, want %v on %v", cap.react, cap.ids, wantReact, wantIDs)
	}
	for i := range wantReact {
		if cap.react[i] != wantReact[i] || cap.ids[i] != wantIDs[i] {
			t.Fatalf("reactions = %v %v, want %v on %v", cap.react, cap.ids, wantReact, wantIDs)
		}
	}
	if cap.texts != 1 {
		t.Fatalf("text replies = %d, want 1", cap.texts)
	}
}

// TestDebounceSeparateTurns: messages further apart than the window are
// independent turns.
func TestDebounceSeparateTurns(t *testing.T) {
	core := &turnCapture{}
	w := newTestWhatsApp(t, core)
	w.debounce = 80 * time.Millisecond
	w.reactions = false

	w.enqueue("34600111222", "wamid.1", "hola", "")
	time.Sleep(250 * time.Millisecond)
	w.enqueue("34600111222", "wamid.2", "mundo", "")
	time.Sleep(250 * time.Millisecond)

	turns := core.turns()
	if len(turns) != 2 || turns[0] != "hola" || turns[1] != "mundo" {
		t.Fatalf("turns = %q, want two independent turns [hola mundo]", turns)
	}
}

// TestDebounceOff: with debounce disabled every message is answered on
// arrival (the old parallel behavior).
func TestDebounceOff(t *testing.T) {
	core := &turnCapture{}
	w := newTestWhatsApp(t, core)
	w.debounceOn = false
	w.reactions = false

	w.enqueue("34600111222", "wamid.1", "hola", "")
	w.enqueue("34600111222", "wamid.2", "mundo", "")
	time.Sleep(200 * time.Millisecond)

	turns := core.turns()
	if len(turns) != 2 {
		t.Fatalf("turns = %q, want 2 immediate turns", turns)
	}
	joined := turns[0] + "|" + turns[1]
	if !strings.Contains(joined, "hola") || !strings.Contains(joined, "mundo") {
		t.Fatalf("lost a message with debounce off: %q", turns)
	}
}

// TestInboundReactionIgnored: an incoming emoji reaction is feedback,
// not a turn: it must never reach the agent (a reaction used to trigger
// a full model run and a reply).
func TestInboundReactionIgnored(t *testing.T) {
	fc := &fakeCore{}
	w := newTestWhatsApp(t, fc)
	w.debounce = 10 * time.Millisecond
	body := `{"entry":[{"changes":[{"value":{"messages":[{"from":"34600123456","id":"wamid.r1","type":"reaction","reaction":{"message_id":"wamid.out1","emoji":"\ud83d\udc4d"}}]}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook/whatsapp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	time.Sleep(300 * time.Millisecond)
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.text != "" {
		t.Fatalf("reaction reached the agent as a turn: %q", fc.text)
	}
}
