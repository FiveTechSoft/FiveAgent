package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenStoreRoundTrip(t *testing.T) {
	store := &TokenStore{Path: filepath.Join(t.TempDir(), "tokens.json")}
	tok := &Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour).Truncate(time.Second)}
	if err := store.Save("gmail", tok); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file perms: %o", fi.Mode().Perm())
	}
	got, err := store.Load("gmail")
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "a1" || got.RefreshToken != "r1" {
		t.Fatalf("loaded %+v", got)
	}
	if _, err := store.Load("slack"); err == nil {
		t.Fatal("expected missing-token error")
	}
}

// fakeTokenServer asserts the OAuth form fields and issues tokens.
func fakeTokenServer(t *testing.T, refreshes *int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("client_id") != "cid" || r.Form.Get("client_secret") != "csecret" {
			t.Errorf("client credentials: %v", r.Form)
		}
		rw.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "the-code" || r.Form.Get("redirect_uri") == "" {
				t.Errorf("exchange form: %v", r.Form)
			}
			json.NewEncoder(rw).Encode(map[string]any{
				"access_token": "access-1", "refresh_token": "refresh-1", "expires_in": 3600,
			})
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh-1" {
				t.Errorf("refresh form: %v", r.Form)
			}
			*refreshes++
			json.NewEncoder(rw).Encode(map[string]any{"access_token": "access-2", "expires_in": 3600})
		default:
			t.Errorf("grant_type %q", r.Form.Get("grant_type"))
			rw.WriteHeader(http.StatusBadRequest)
		}
	}))
}

// The full connect round against fake endpoints: start redirects with
// a state, the callback exchanges and saves the token, and a wrong
// state is rejected. No real account is ever involved.
func TestHandlerConnectRound(t *testing.T) {
	var refreshes int
	tokSrv := fakeTokenServer(t, &refreshes)
	t.Cleanup(tokSrv.Close)

	store := &TokenStore{Path: filepath.Join(t.TempDir(), "tokens.json")}
	cfg := Config{
		ClientID: "cid", ClientSecret: "csecret",
		AuthURL: "http://provider.example/auth", TokenURL: tokSrv.URL,
		Scopes: []string{"scope-a"},
	}
	h := NewHandler(map[string]Config{"gmail": cfg}, store,
		func(name string) string { return "http://bot.example/oauth/" + name + "/callback" })

	// start: redirect carries client id, scope and a state.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/gmail/start", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("start: %d", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Host != "provider.example" {
		t.Fatalf("redirect host %s", loc.Host)
	}
	q := loc.Query()
	if q.Get("client_id") != "cid" || q.Get("scope") != "scope-a" || q.Get("state") == "" {
		t.Fatalf("redirect query: %s", q)
	}
	state := q.Get("state")

	// wrong state: rejected, no token stored.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/gmail/callback?state=wrong&code=the-code", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bad state: %d", rec.Code)
	}
	if _, err := store.Load("gmail"); err == nil {
		t.Fatal("token stored despite bad state")
	}

	// right state: token exchanged and stored.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/gmail/callback?state="+state+"&code=the-code", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("callback: %d", rec.Code)
	}
	tok, err := store.Load("gmail")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-1" || tok.RefreshToken != "refresh-1" {
		t.Fatalf("stored %+v", tok)
	}
	if !strings.Contains(rec.Body.String(), "gmail connected") {
		t.Fatalf("callback page: %q", rec.Body.String())
	}
}

// An expired token is refreshed transparently and saved back; the
// request goes out with the fresh access token.
func TestClientRefreshesExpiredToken(t *testing.T) {
	var refreshes int
	tokSrv := fakeTokenServer(t, &refreshes)
	t.Cleanup(tokSrv.Close)

	store := &TokenStore{Path: filepath.Join(t.TempDir(), "tokens.json")}
	if err := store.Save("gmail", &Token{
		AccessToken: "stale", RefreshToken: "refresh-1",
		Expiry: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{ClientID: "cid", ClientSecret: "csecret", TokenURL: tokSrv.URL}
	cl, err := Client(context.Background(), cfg, store, "gmail")
	if err != nil {
		t.Fatal(err)
	}

	var gotAuth string
	api := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		rw.Write([]byte(`{}`))
	}))
	t.Cleanup(api.Close)
	if _, err := cl.Get(api.URL); err != nil {
		t.Fatal(err)
	}
	if refreshes != 1 {
		t.Fatalf("refreshes: %d", refreshes)
	}
	if gotAuth != "Bearer access-2" {
		t.Fatalf("auth header %q", gotAuth)
	}
	saved, err := store.Load("gmail")
	if err != nil {
		t.Fatal(err)
	}
	if saved.AccessToken != "access-2" || saved.RefreshToken != "refresh-1" {
		t.Fatalf("saved %+v", saved)
	}
}
