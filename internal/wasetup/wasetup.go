// Package wasetup configures the WhatsApp Cloud API side of a FiveAgent
// install through the Graph API: it checks the access token, confirms the
// phone number id, registers the webhook callback and subscribes the app
// to the WhatsApp Business Account. Every step reports what it did or why
// it could not; nothing is assumed done without the API saying so.
package wasetup

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

// DefaultBase is the Graph API root used outside tests.
const DefaultBase = "https://graph.facebook.com/v21.0"

// Client talks to the Graph API.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// Step is the outcome of one setup step.
type Step struct {
	Name   string
	OK     bool
	Detail string
}

func (c *Client) base() string {
	if c.Base == "" {
		return DefaultBase
	}
	return strings.TrimRight(c.Base, "/")
}

func (c *Client) do(ctx context.Context, method, path string, form url.Values, token string) (map[string]any, error) {
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 20 * time.Second}
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(raw))
		if e, ok := out["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok {
				msg = m
			}
		}
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("graph %s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	return out, nil
}

// CheckPhone reads the phone number id with the access token. A success
// proves both the token and the id; it does not prove the webhook.
func (c *Client) CheckPhone(ctx context.Context, phoneID string) Step {
	s := Step{Name: "token and phone number id"}
	if c.Token == "" || phoneID == "" {
		s.Detail = "access_token and phone_number_id are required"
		return s
	}
	out, err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(phoneID)+"?fields=display_phone_number,verified_name", nil, c.Token)
	if err != nil {
		s.Detail = err.Error()
		return s
	}
	s.OK = true
	s.Detail = fmt.Sprintf("number %v, name %v", out["display_phone_number"], out["verified_name"])
	return s
}

// SubscribeWABA subscribes the app behind the token to the account, so
// messages reach the webhook.
func (c *Client) SubscribeWABA(ctx context.Context, wabaID string) Step {
	s := Step{Name: "subscribe app to WhatsApp Business Account"}
	if wabaID == "" {
		s.Detail = "waba id is required"
		return s
	}
	if _, err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(wabaID)+"/subscribed_apps", url.Values{}, c.Token); err != nil {
		s.Detail = err.Error()
		return s
	}
	// Read back: the POST answering success is not the same as being
	// subscribed.
	out, err := c.do(ctx, http.MethodGet, "/"+url.PathEscape(wabaID)+"/subscribed_apps", nil, c.Token)
	if err != nil {
		s.Detail = "subscribed, but the read-back failed: " + err.Error()
		return s
	}
	if data, ok := out["data"].([]any); ok && len(data) > 0 {
		s.OK = true
		s.Detail = fmt.Sprintf("%d app(s) subscribed (read back)", len(data))
		return s
	}
	s.Detail = "the API accepted the call but the read-back lists no subscribed app"
	return s
}

// RegisterWebhook sets the callback URL and verify token for the
// WhatsApp Business Account object and the messages field. It needs the
// app id and the app secret (an app access token), not the user token.
func (c *Client) RegisterWebhook(ctx context.Context, appID, appSecret, callbackURL, verifyToken string) Step {
	s := Step{Name: "register webhook callback"}
	if appID == "" || appSecret == "" || callbackURL == "" || verifyToken == "" {
		s.Detail = "app id, app secret, callback url and verify token are required"
		return s
	}
	if !strings.HasPrefix(callbackURL, "https://") {
		s.Detail = "callback url must be https"
		return s
	}
	form := url.Values{}
	form.Set("object", "whatsapp_business_account")
	form.Set("callback_url", callbackURL)
	form.Set("verify_token", verifyToken)
	form.Set("fields", "messages")
	if _, err := c.do(ctx, http.MethodPost, "/"+url.PathEscape(appID)+"/subscriptions", form, appID+"|"+appSecret); err != nil {
		s.Detail = err.Error()
		return s
	}
	s.OK = true
	s.Detail = "callback registered for the messages field (Meta verified the URL answers the challenge)"
	return s
}
