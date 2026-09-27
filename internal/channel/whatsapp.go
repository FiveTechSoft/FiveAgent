package channel

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

// whatsapp is the official WhatsApp Cloud API adapter.
// Inbound: Meta POSTs message webhooks to ListenAddr. Outbound: Graph API.
type whatsapp struct {
	cfg  config.Channel
	core *agent.Agent
	mux  *http.ServeMux
}

// NewWhatsApp builds the WhatsApp Cloud API adapter.
// Endpoint docs: https://developers.facebook.com/docs/whatsapp/cloud-api
func NewWhatsApp(cfg config.Channel, core *agent.Agent) Channel {
	w := &whatsapp{cfg: cfg, core: core, mux: http.NewServeMux()}
	w.mux.HandleFunc("/webhook/whatsapp", w.handleWebhook)
	return w
}

func (w *whatsapp) Name() string { return "whatsapp" }

// Run serves the webhook endpoint until ctx is cancelled.
func (w *whatsapp) Run(ctx context.Context) error {
	addr := w.cfg.ListenAddr
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{Addr: addr, Handler: w.mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	log.Printf("whatsapp webhook listening on %s/webhook/whatsapp", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (w *whatsapp) handleWebhook(rw http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.verify(rw, r)
	case http.MethodPost:
		w.inbound(rw, r)
	default:
		http.Error(rw, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// verify answers Meta's webhook handshake:
// GET ?hub.mode=subscribe&hub.verify_token=...&hub.challenge=...
func (w *whatsapp) verify(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") == "subscribe" &&
		subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(w.cfg.VerifyToken)) == 1 {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(q.Get("hub.challenge")))
		return
	}
	http.Error(rw, "forbidden", http.StatusForbidden)
}

// webhookPayload is the subset of the Cloud API payload we use.
type webhookPayload struct {
	Entry []struct {
		Changes []struct {
			Value struct {
				Messages []struct {
					From string `json:"from"`
					ID   string `json:"id"`
					Type string `json:"type"`
					Text struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// inbound receives message notifications. Always 200 fast, process async,
// per Meta's requirement (they retry on non-200).
func (w *whatsapp) inbound(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		rw.WriteHeader(http.StatusOK)
		return
	}
	rw.WriteHeader(http.StatusOK)

	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Printf("whatsapp: bad payload: %v", err)
		return
	}
	for _, e := range p.Entry {
		for _, ch := range e.Changes {
			for _, m := range ch.Value.Messages {
				if m.Type != "text" || strings.TrimSpace(m.Text.Body) == "" {
					continue
				}
				from, text := m.From, m.Text.Body
				go w.answer(from, text)
			}
		}
	}
}

// answer runs the agent and sends the reply via the Graph API.
func (w *whatsapp) answer(to, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	reply, err := w.core.Handle(ctx, "whatsapp", to, text)
	if err != nil {
		log.Printf("whatsapp: agent: %v", err)
		reply = "Lo siento, algo ha fallado. Inténtalo de nuevo en un momento."
	}
	if err := w.send(ctx, to, reply); err != nil {
		log.Printf("whatsapp: send: %v", err)
	}
}

// send posts a text message through the Graph API.
func (w *whatsapp) send(ctx context.Context, to, text string) error {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": text},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://graph.facebook.com/v21.0/%s/messages", w.cfg.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(b)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.cfg.AccessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("graph api: %s: %s", resp.Status, raw)
	}
	return nil
}
