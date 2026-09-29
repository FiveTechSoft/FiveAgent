package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// Stage 23: the browser - numbered-element pages, by-id actions,
// and the confirmation gate. These are the battery's "web" cases.

const shopPage = `<html><head><title>Test Shop</title></head><body>
<h1>Test Shop</h1>
<p>Welcome to the test shop. Buy things here.</p>
<a href="/next">catalog</a>
<form action="/buy" method="post">
<input type="text" name="user" placeholder="your name">
<input type="password" name="card">
<button type="submit">Pay now</button>
</form>
</body></html>`

// shopServer serves the form and records what reaches /buy.
type shopServer struct {
	mu    sync.Mutex
	posts []string
	srv   *httptest.Server
}

func newShopServer(t *testing.T) *shopServer {
	t.Helper()
	s := &shopServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, shopPage)
	})
	mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Catalog</title></head><body><p>catalog page</p></body></html>`)
	})
	mux.HandleFunc("/buy", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.posts = append(s.posts, r.Method+" "+r.Form.Encode())
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Bought</title></head><body><p>order placed</p></body></html>`)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}
func (s *shopServer) gotPosts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.posts...)
}

func browserCtx() context.Context {
	return tools.WithRequestInfo(context.Background(), "whatsapp", "user1")
}

// TestWebFormEndToEnd: browse, fill, submit is GATED, confirm sends.
// The battery's "web" category case: a local form end-to-end.
func TestWebFormEndToEnd(t *testing.T) {
	shop := newShopServer(t)
	br := &tools.Browser{}
	ctx := browserCtx()

	// Browse: numbered elements present.
	out, err := tools.BrowsePage{B: br}.Execute(ctx, json.RawMessage(`{"url":"`+shop.srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"title: Test Shop", "#1 link", "#2 input", "#3 input", "#4 button"} {
		if !strings.Contains(out, want) {
			t.Fatalf("listing missing %q:\n%s", want, out)
		}
	}

	// Fill the fields (link is #1, inputs #2/#3, button #4).
	if _, err := (tools.BrowserAct{B: br}).Execute(ctx, json.RawMessage(`{"action":"fill","id":2,"value":"olin"}`)); err != nil {
		t.Fatal(err)
	}
	pw, err := tools.BrowserAct{B: br}.Execute(ctx, json.RawMessage(`{"action":"fill","id":3,"value":"secret123"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pw, "secret123") {
		t.Fatalf("password value leaked in the tool result: %q", pw)
	}

	// Click the submit button: the gate must fire, NOTHING is sent.
	gate, err := tools.BrowserAct{B: br}.Execute(ctx, json.RawMessage(`{"action":"click","id":4}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gate, "NEEDS USER CONFIRMATION") {
		t.Fatalf("submit was not gated: %q", gate)
	}
	if strings.Contains(gate, "secret123") {
		t.Fatalf("password value leaked in the gate summary: %q", gate)
	}
	if n := len(shop.gotPosts()); n != 0 {
		t.Fatalf("the form reached the server WITHOUT confirmation (%d posts)", n)
	}

	// Wrong token: still nothing.
	if _, err := (tools.BrowserAct{B: br}).Execute(ctx, json.RawMessage(`{"action":"confirm","token":"wrong"}`)); err == nil {
		t.Fatal("a wrong token must not confirm")
	}
	if n := len(shop.gotPosts()); n != 0 {
		t.Fatalf("wrong token submitted the form (%d posts)", n)
	}

	// Correct token: exactly one POST with the filled values.
	token := gate[strings.Index(gate, `token "`)+7:]
	token = token[:strings.Index(token, `"`)]
	out, err = tools.BrowserAct{B: br}.Execute(ctx, json.RawMessage(`{"action":"confirm","token":"`+token+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "submitted") || !strings.Contains(out, "order placed") {
		t.Fatalf("confirm result: %q", out)
	}
	posts := shop.gotPosts()
	if len(posts) != 1 || !strings.Contains(posts[0], "user=olin") || !strings.Contains(posts[0], "card=secret123") {
		t.Fatalf("expected exactly one POST with the filled values, got %v", posts)
	}

	// The token is single-use.
	if _, err := (tools.BrowserAct{B: br}).Execute(ctx, json.RawMessage(`{"action":"confirm","token":"`+token+`"}`)); err == nil {
		t.Fatal("token reused")
	}
	if n := len(shop.gotPosts()); n != 1 {
		t.Fatalf("reused token submitted again (%d posts)", n)
	}
}

// TestWebLinkNavigation: clicking a link follows it.
func TestWebLinkNavigation(t *testing.T) {
	shop := newShopServer(t)
	br := &tools.Browser{}
	ctx := browserCtx()
	if _, err := (tools.BrowsePage{B: br}).Execute(ctx, json.RawMessage(`{"url":"`+shop.srv.URL+`"}`)); err != nil {
		t.Fatal(err)
	}
	out, err := tools.BrowserAct{B: br}.Execute(ctx, json.RawMessage(`{"action":"click","id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "title: Catalog") {
		t.Fatalf("link click did not navigate: %q", out)
	}
}

// TestWebAuditRedaction: every action is audit-logged and the
// password value appears NOWHERE in the log.
func TestWebAuditRedaction(t *testing.T) {
	shop := newShopServer(t)
	dir := t.TempDir()
	br := &tools.Browser{AuditDir: dir}
	ctx := browserCtx()
	exec := func(tool tools.Tool, args string) {
		t.Helper()
		if _, err := tool.Execute(ctx, json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}
	exec(tools.BrowsePage{B: br}, `{"url":"`+shop.srv.URL+`"}`)
	exec(tools.BrowserAct{B: br}, `{"action":"fill","id":2,"value":"olin"}`)
	exec(tools.BrowserAct{B: br}, `{"action":"fill","id":3,"value":"secret123"}`)
	gate, err := tools.BrowserAct{B: br}.Execute(ctx, json.RawMessage(`{"action":"click","id":4}`))
	if err != nil {
		t.Fatal(err)
	}
	token := gate[strings.Index(gate, `token "`)+7:]
	token = token[:strings.Index(token, `"`)]
	exec(tools.BrowserAct{B: br}, `{"action":"confirm","token":"`+token+`"}`)

	b, err := os.ReadFile(filepath.Join(dir, "whatsapp-user1.log"))
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	for _, want := range []string{"open http://", "fill #2", "fill #3", "submit gated", "CONFIRMED"} {
		if !strings.Contains(log, want) {
			t.Fatalf("audit log missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "secret123") {
		t.Fatalf("password value in the audit log:\n%s", log)
	}
}
