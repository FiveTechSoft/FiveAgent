package oauth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Handler serves /oauth/{provider}/start and /oauth/{provider}/callback
// on a channel's HTTP mux (the same pattern MountLinks uses, stage 24).
// start redirects to the provider consent screen with a random state;
// callback verifies the state, exchanges the code and stores the
// token. States live in memory with a short TTL: a restart mid-connect
// just means starting over.
type Handler struct {
	Providers  map[string]Config
	Store      *TokenStore
	RedirectUR func(name string) string // redirect URI per provider

	mu     sync.Mutex
	states map[string]time.Time
}

// NewHandler builds the connect handler.
func NewHandler(providers map[string]Config, store *TokenStore, redirectUR func(name string) string) *Handler {
	return &Handler{Providers: providers, Store: store, RedirectUR: redirectUR, states: map[string]time.Time{}}
}

// ServeHTTP routes /oauth/{name}/start and /oauth/{name}/callback.
func (h *Handler) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/oauth/")
	name, action, _ := strings.Cut(rest, "/")
	cfg, ok := h.Providers[name]
	if !ok || name == "" {
		http.NotFound(rw, r)
		return
	}
	switch action {
	case "start":
		state := randomState()
		h.mu.Lock()
		h.states[state] = time.Now().Add(10 * time.Minute)
		for s, exp := range h.states {
			if time.Now().After(exp) {
				delete(h.states, s)
			}
		}
		h.mu.Unlock()
		http.Redirect(rw, r, AuthCodeURL(cfg, h.RedirectUR(name), state), http.StatusFound)
	case "callback":
		h.callback(rw, r, name, cfg)
	default:
		http.NotFound(rw, r)
	}
}

func (h *Handler) callback(rw http.ResponseWriter, r *http.Request, name string, cfg Config) {
	state := r.URL.Query().Get("state")
	h.mu.Lock()
	exp, ok := h.states[state]
	delete(h.states, state) // single use
	h.mu.Unlock()
	if !ok || time.Now().After(exp) {
		log.Printf("oauth: %s callback with unknown or expired state", name)
		http.Error(rw, "invalid state", http.StatusForbidden)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(rw, "missing code", http.StatusBadRequest)
		return
	}
	tok, err := Exchange(r.Context(), cfg, h.RedirectUR(name), code)
	if err != nil {
		log.Printf("oauth: %s exchange failed: %v", name, err)
		http.Error(rw, "token exchange failed", http.StatusBadGateway)
		return
	}
	if err := h.Store.Save(name, tok); err != nil {
		log.Printf("oauth: %s token save failed: %v", name, err)
		http.Error(rw, "token save failed", http.StatusInternalServerError)
		return
	}
	log.Printf("oauth: %s connected", name)
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(rw, "<html><body><h1>%s connected</h1><p>You can close this tab.</p></body></html>", name)
}

func randomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
