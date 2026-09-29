package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/links"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Stage 24: links - signed, PIN-protected report and form links.
// The battery's "links" cases. The minted public base is rewritten
// to a local httptest server running the same handler.

type logBuf struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}
func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

type linkEnv struct {
	svc *links.Service
	lb  *logBuf
	dir string
	ts  *httptest.Server
}

func newLinkEnv(t *testing.T) *linkEnv {
	t.Helper()
	dir := t.TempDir()
	lb := &logBuf{}
	svc, err := links.Open(
		filepath.Join(dir, "secret"),
		"https://bot.example.com",
		filepath.Join(dir, "links"),
		filepath.Join(dir, "vault"),
		lb.logf,
	)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(svc.Handler())
	t.Cleanup(ts.Close)
	return &linkEnv{svc: svc, lb: lb, dir: dir, ts: ts}
}

// local rewrites a minted public link to the test server.
func (e *linkEnv) local(link string) string {
	return strings.Replace(link, "https://bot.example.com", e.ts.URL, 1)
}

func (e *linkEnv) get(t *testing.T, link, pin string) (string, int) {
	t.Helper()
	u := e.local(link)
	if pin != "" {
		u += "?pin=" + pin
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b), resp.StatusCode
}

func extractLinkPIN(t *testing.T, toolOut string) (string, string) {
	t.Helper()
	var link, pin string
	for _, line := range strings.Split(toolOut, "\n") {
		if strings.HasPrefix(line, "link: ") {
			link = strings.TrimPrefix(line, "link: ")
		}
		if strings.HasPrefix(line, "PIN: ") {
			pin = strings.TrimPrefix(line, "PIN: ")
		}
	}
	if link == "" || pin == "" {
		t.Fatalf("tool result must carry link and PIN: %q", toolOut)
	}
	return link, pin
}

// TestLinksReportLifecycle: (i) a report link renders the report, and
// is rejected after expiry and with a wrong signature.
func TestLinksReportLifecycle(t *testing.T) {
	e := newLinkEnv(t)
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "user1")
	out, err := tools.MakeReportLink{S: e.svc}.Execute(ctx, json.RawMessage(`{"title":"Battery report","content":"M1 7/7 - all green"}`))
	if err != nil {
		t.Fatal(err)
	}
	link, pin := extractLinkPIN(t, out)

	if _, code := e.get(t, link, ""); code != http.StatusForbidden {
		t.Fatalf("no PIN must be rejected, got %d", code)
	}
	if _, code := e.get(t, link, "0000"); code != http.StatusForbidden {
		t.Fatalf("wrong PIN must be rejected, got %d", code)
	}
	body, code := e.get(t, link, pin)
	if code != http.StatusOK || !strings.Contains(body, "Battery report") || !strings.Contains(body, "M1 7/7 - all green") {
		t.Fatalf("report must render with the right PIN (%d): %s", code, body)
	}
	tampered := link[:len(link)-2] + "XX"
	if _, code := e.get(t, tampered, pin); code != http.StatusForbidden {
		t.Fatalf("tampered signature must be rejected, got %d", code)
	}
	e.svc.SetNow(func() time.Time { return time.Now().Add(48 * time.Hour) })
	if _, code := e.get(t, link, pin); code != http.StatusGone {
		t.Fatalf("expired link must be rejected with 410, got %d", code)
	}
}

// TestLinksFormToVault: (ii) a form submission lands in the vault and
// the value appears in NO log line.
func TestLinksFormToVault(t *testing.T) {
	e := newLinkEnv(t)
	ctx := tools.WithRequestInfo(context.Background(), "whatsapp", "user1")
	out, err := tools.MakeFormLink{S: e.svc}.Execute(ctx, json.RawMessage(`{"title":"Token de Telegram","vault_key":"telegram_bot_token"}`))
	if err != nil {
		t.Fatal(err)
	}
	link, pin := extractLinkPIN(t, out)

	body, code := e.get(t, link, pin)
	if code != http.StatusOK || !strings.Contains(body, "Token de Telegram") || !strings.Contains(body, "<form") {
		t.Fatalf("form must render (%d): %s", code, body)
	}
	secretValue := "123456:ABC-supersecret-token"
	resp, err := http.PostForm(e.local(link)+"?pin="+pin, url.Values{"secret": {secretValue}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("form POST failed: %d", resp.StatusCode)
	}
	b, err := os.ReadFile(filepath.Join(e.dir, "vault", "telegram_bot_token.secret"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != secretValue {
		t.Fatalf("vault content: %q", b)
	}
	logs := e.lb.String()
	if strings.Contains(logs, secretValue) || strings.Contains(logs, "supersecret") {
		t.Fatalf("submitted value in the logs:\n%s", logs)
	}
	if !strings.Contains(logs, "never logged") {
		t.Fatalf("expected the redacted audit line, got:\n%s", logs)
	}
}

// TestLinksSenderBinding: (iii) the signature binds the sender - a
// link minted for one sender does not verify once the record names
// another (the PIN itself never leaves the first sender's chat).
func TestLinksSenderBinding(t *testing.T) {
	e := newLinkEnv(t)
	link, pin, err := e.svc.MintReport("user1", "Private", "for user1 only")
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Split(strings.TrimPrefix(link, "https://bot.example.com/l/"), "/")[0]
	recPath := filepath.Join(e.dir, "links", id+".json")
	b, err := os.ReadFile(recPath)
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(b), `"sender":"user1"`, `"sender":"user2"`, 1)
	if forged == string(b) {
		t.Fatal("record must contain the sender")
	}
	if err := os.WriteFile(recPath, []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, code := e.get(t, link, pin); code != http.StatusForbidden {
		t.Fatalf("a link opened against another sender's record must be rejected, got %d", code)
	}
}
