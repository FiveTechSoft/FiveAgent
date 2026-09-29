// Package links serves report pages and sensitive-data forms over the
// bot's own HTTP listener (stage 24), so answers arrive as links, not
// walls of text. Security by design: every link is HMAC-signed, binds
// the sender it was minted for, and expires; a per-chat PIN (sent
// alongside the link) is the second factor - another sender opening
// the link does not have it; submitted form values land in the vault
// and never touch a log line; the server binds wherever the channel
// adapter already listens (localhost by default).
package links

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Record is one minted link. Reports carry a content file; forms
// carry a vault key and a label.
type Record struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // report | form
	Sender   string    `json:"sender"`
	PINHash  string    `json:"pin_hash"`
	Expires  time.Time `json:"expires"`
	Title    string    `json:"title"`
	VaultKey string    `json:"vault_key,omitempty"`
}

// Service mints and serves links.
type Service struct {
	mu       sync.Mutex
	secret   []byte
	baseURL  string // public base, e.g. the tunnel URL
	storeDir string // report HTML + records
	vaultDir string // form submissions
	ttl      time.Duration
	logf     func(string, ...any) // audit; NEVER receives submitted values
	now      func() time.Time
}

// Open loads (or creates) the signing secret and the store.
// baseURL is the public base the bot's links are reached on.
func Open(secretPath, baseURL, storeDir, vaultDir string, logf func(string, ...any)) (*Service, error) {
	if baseURL == "" {
		return nil, errors.New("links: base_url is required (the public URL the bot is reached on)")
	}
	baseURL = strings.TrimRight(baseURL, "/")
	secret, err := loadOrCreateSecret(secretPath)
	if err != nil {
		return nil, err
	}
	for _, d := range []string{storeDir, vaultDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{
		secret: secret, baseURL: baseURL, storeDir: storeDir,
		vaultDir: vaultDir, ttl: 24 * time.Hour, logf: logf,
		now: time.Now,
	}, nil
}

// SetNow overrides the clock (tests).
func (s *Service) SetNow(f func() time.Time) { s.now = f }

func loadOrCreateSecret(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil {
		return b, nil
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b[:], 0o600); err != nil {
		return nil, err
	}
	return b[:], nil
}

func (s *Service) sign(parts ...string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(strings.Join(parts, "|")))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:26]
}

// MintReport stores title/content as an HTML page and returns the
// link and its PIN for sender.
func (s *Service) MintReport(sender, title, content string) (link, pin string, err error) {
	rec, pin, err := s.newRecord("report", sender, title, "")
	if err != nil {
		return "", "", err
	}
	page := "<!doctype html><html><head><meta charset=utf-8><title>" + html.EscapeString(title) +
		"</title></head><body style=\"font-family:sans-serif;max-width:46em;margin:2em auto;padding:0 1em\">" +
		"<h1>" + html.EscapeString(title) + "</h1><pre style=\"white-space:pre-wrap\">" +
		html.EscapeString(content) + "</pre></body></html>"
	if err := os.WriteFile(s.pagePath(rec.ID), []byte(page), 0o600); err != nil {
		return "", "", err
	}
	if err := s.saveRecord(rec); err != nil {
		return "", "", err
	}
	s.logf("mint report %q for %s (id %s)", title, sender, rec.ID)
	return s.url(rec), pin, nil
}

// MintForm creates a sensitive-data form landing in vaultKey and
// returns the link and its PIN for sender.
func (s *Service) MintForm(sender, title, vaultKey string) (link, pin string, err error) {
	if vaultKey == "" || strings.ContainsAny(vaultKey, "/\\") {
		return "", "", errors.New("links: bad vault key")
	}
	rec, pin, err := s.newRecord("form", sender, title, vaultKey)
	if err != nil {
		return "", "", err
	}
	if err := s.saveRecord(rec); err != nil {
		return "", "", err
	}
	s.logf("mint form %q for %s (id %s)", title, sender, rec.ID)
	return s.url(rec), pin, nil
}

func (s *Service) newRecord(kind, sender, title, vaultKey string) (*Record, string, error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, "", err
	}
	var pinB [2]byte
	if _, err := rand.Read(pinB[:]); err != nil {
		return nil, "", err
	}
	pin := fmt.Sprintf("%04d", int(pinB[0])<<8|int(pinB[1]))
	pin = pin[len(pin)-4:]
	ph := sha256.Sum256([]byte(pin))
	return &Record{
		ID: hex.EncodeToString(rnd[:]), Kind: kind, Sender: sender,
		PINHash: hex.EncodeToString(ph[:]), Expires: s.now().Add(s.ttl),
		Title: title, VaultKey: vaultKey,
	}, pin, nil
}

func (s *Service) url(rec *Record) string {
	return fmt.Sprintf("%s/l/%s/%s", s.baseURL, rec.ID, s.sign(rec.ID, rec.Sender, rec.PINHash, rec.Expires.Format(time.RFC3339)))
}

func (s *Service) pagePath(id string) string   { return filepath.Join(s.storeDir, id+".html") }
func (s *Service) recPath(id string) string    { return filepath.Join(s.storeDir, id+".json") }
func (s *Service) vaultPath(key string) string { return filepath.Join(s.vaultDir, key+".secret") }

func (s *Service) saveRecord(rec *Record) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return os.WriteFile(s.recPath(rec.ID), b, 0o600)
}

// Handler serves the /l/ routes; mount it on the channel listener.
func (s *Service) Handler() http.Handler { return http.HandlerFunc(s.serve) }

// serve handles GET /l/<id>/<sig> (page or form), GET/POST with
// ?pin= (auth), and POST /l/<id>/<sig>?pin= (form submission).
func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/l/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rec, ok := s.loadAndVerify(parts[0], parts[1])
	if !ok {
		s.logf("rejected link access (bad id or signature)")
		http.Error(w, "link not valid", http.StatusForbidden)
		return
	}
	if !s.now().Before(rec.Expires) {
		s.logf("rejected expired link (id %s)", rec.ID)
		http.Error(w, "link expired", http.StatusGone)
		return
	}
	pin := r.URL.Query().Get("pin")
	ph := sha256.Sum256([]byte(pin))
	if hex.EncodeToString(ph[:]) != rec.PINHash {
		// Second factor: another sender does not have the PIN that
		// went to the chat of the sender this was minted for.
		s.logf("rejected link access: wrong or missing PIN (id %s)", rec.ID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "<!doctype html><title>Forbidden</title><p>This link is not for you, or the PIN is missing.</p>")
		return
	}

	switch rec.Kind {
	case "report":
		b, err := os.ReadFile(s.pagePath(rec.ID))
		if err != nil {
			http.Error(w, "report gone", http.StatusNotFound)
			return
		}
		s.logf("served report (id %s)", rec.ID)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	case "form":
		s.serveForm(w, r, rec)
	default:
		http.Error(w, "unknown link kind", http.StatusInternalServerError)
	}
}

func (s *Service) serveForm(w http.ResponseWriter, r *http.Request, rec *Record) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodGet {
		fmt.Fprintf(w, `<!doctype html><html><head><meta charset=utf-8><title>%s</title></head>
<body style="font-family:sans-serif;max-width:30em;margin:2em auto;padding:0 1em">
<h1>%s</h1>
<form method="post">
<p><input type="password" name="secret" style="width:100%%;padding:.5em" autocomplete="off"></p>
<p><button type="submit">Enviar</button></p>
</form>
<p><small>El valor va directo al vault del bot; nunca pasa por el chat.</small></p>
</body></html>`, html.EscapeString(rec.Title), html.EscapeString(rec.Title))
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	value := r.PostForm.Get("secret")
	if value == "" {
		http.Error(w, "empty value", http.StatusBadRequest)
		return
	}
	// The value goes to the vault and NOWHERE else: not logs, not
	// the audit trail, not the reply beyond a fixed confirmation.
	if err := os.WriteFile(s.vaultPath(rec.VaultKey), []byte(value), 0o600); err != nil {
		http.Error(w, "cannot store", http.StatusInternalServerError)
		return
	}
	s.logf("form submission stored in vault key %q (id %s; value never logged)", rec.VaultKey, rec.ID)
	fmt.Fprint(w, `<!doctype html><title>Guardado</title><p>Guardado. Ya puedes cerrar esta página.</p>`)
}

func (s *Service) loadAndVerify(id, sig string) (*Record, bool) {
	b, err := os.ReadFile(s.recPath(id))
	if err != nil {
		return nil, false
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, false
	}
	want := s.sign(rec.ID, rec.Sender, rec.PINHash, rec.Expires.Format(time.RFC3339))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return nil, false
	}
	return &rec, true
}
