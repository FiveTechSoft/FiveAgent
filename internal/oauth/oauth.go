// Package oauth is the shared integration pattern (roadmap stage 27):
// authorization-code connect, token exchange, refresh, and a small
// on-disk token store, so each service (Gmail, Calendar, Drive, ...)
// is a known shape instead of a new design. Endpoints are package
// variables so tests point them at fake servers - no real account is
// ever touched in CI.
//
// Tokens are stored as a JSON file with 0600 permissions and atomic
// tmp+rename writes. They are NOT encrypted at rest: that is roadmap
// stage 8 (AES-256-GCM) and this store declares the dependency rather
// than faking crypto.
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config is one provider's OAuth app registration.
type Config struct {
	ClientID     string
	ClientSecret string
	AuthURL      string   // authorization endpoint
	TokenURL     string   // token endpoint
	Scopes       []string // requested scopes
	// TokenHeaders are extra headers on token endpoint requests.
	// GitHub's token endpoint answers form-encoded unless asked for
	// JSON ("Accept": "application/json") - declared here instead of
	// special-casing a vendor inside the shared flow.
	TokenHeaders map[string]string
}

// Token is an OAuth2 token set.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

// AuthCodeURL builds the consent URL the user opens.
func AuthCodeURL(cfg Config, redirectURI, state string) string {
	q := url.Values{
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {strings.Join(cfg.Scopes, " ")},
		"access_type":   {"offline"},
		"state":         {state},
	}
	return cfg.AuthURL + "?" + q.Encode()
}

// Exchange trades an authorization code for tokens.
func Exchange(ctx context.Context, cfg Config, redirectURI, code string) (*Token, error) {
	return tokenPost(ctx, cfg, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	})
}

// Refresh trades a refresh token for a fresh access token.
func Refresh(ctx context.Context, cfg Config, refreshToken string) (*Token, error) {
	return tokenPost(ctx, cfg, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

func tokenPost(ctx context.Context, cfg Config, form url.Values) (*Token, error) {
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range cfg.TokenHeaders {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth: %s", resp.Status)
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("oauth: %w", err)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("oauth: empty access token")
	}
	t := &Token{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken}
	if out.ExpiresIn > 0 {
		t.Expiry = time.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return t, nil
}

// Client returns an HTTP client that authorizes requests with the
// stored token, refreshing and saving back when it expired.
func Client(ctx context.Context, cfg Config, store *TokenStore, name string) (*http.Client, error) {
	tok, err := store.Load(name)
	if err != nil {
		return nil, fmt.Errorf("oauth: %s not connected - open the OAuth link: %w", name, err)
	}
	return &http.Client{Transport: &transport{ctx: ctx, cfg: cfg, store: store, name: name, token: tok}}, nil
}

type transport struct {
	ctx   context.Context
	cfg   Config
	store *TokenStore
	name  string
	token *Token
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.token.Expiry.IsZero() && time.Now().After(t.token.Expiry.Add(-time.Minute)) && t.token.RefreshToken != "" {
		fresh, err := Refresh(t.ctx, t.cfg, t.token.RefreshToken)
		if err != nil {
			return nil, fmt.Errorf("oauth: token refresh failed: %w", err)
		}
		if fresh.RefreshToken == "" {
			fresh.RefreshToken = t.token.RefreshToken
		}
		t.token = fresh
		if err := t.store.Save(t.name, fresh); err != nil {
			return nil, fmt.Errorf("oauth: saving refreshed token: %w", err)
		}
	}
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token.AccessToken)
	return http.DefaultTransport.RoundTrip(req)
}
