package channel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

// whatsapp is the official WhatsApp Cloud API adapter.
// Inbound: Meta POSTs webhooks to ListenAddr/webhook/whatsapp.
// Outbound: Graph API v21.0.
type whatsapp struct {
	cfg     config.Channel
	core    Handler
	mux     *http.ServeMux
	http    *http.Client
	baseURL string // graph api base, overridable in tests
	// agentTimeout caps one full agent run; set by Build from the model
	// timeout, defaults to 5 minutes.
	agentTimeout time.Duration
	// ledger records outbound replies before sending (stage 19); nil
	// disables the ledger.
	ledger    *Ledger
	reactions bool // react 👀/✅/⚠️ to inbound messages
	// debounce joins rapid bursts from one sender into a single turn.
	debounce   time.Duration
	debounceOn bool
	mu         sync.Mutex
	pending    map[string]*burst
}

// queuedMsg is one inbound message waiting in a sender's burst.
type queuedMsg struct {
	msgID, text, replyTo string
}

// burst accumulates one sender's rapid messages until the debounce
// window closes.
type burst struct {
	msgs  []queuedMsg
	timer *time.Timer
}

// NewWhatsApp builds the WhatsApp Cloud API adapter.
func NewWhatsApp(cfg config.Channel, core Handler) Channel {
	w := &whatsapp{
		cfg:        cfg,
		core:       core,
		mux:        http.NewServeMux(),
		http:       &http.Client{Timeout: 30 * time.Second},
		baseURL:    "https://graph.facebook.com/v21.0",
		reactions:  cfg.Reactions != "off", // default: status reactions on
		debounce:   3 * time.Second,
		debounceOn: true,
		pending:    make(map[string]*burst),
	}
	switch {
	case cfg.Debounce == "off":
		w.debounceOn = false
	case cfg.Debounce != "":
		if d, err := time.ParseDuration(cfg.Debounce); err == nil && d > 0 {
			w.debounce = d
		} else {
			log.Printf("whatsapp: invalid debounce %q - using 3s", cfg.Debounce)
		}
	}
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
	w.logSecurityWarnings()

	// Stage 19: redeliver replies a crash left unsent, with marker.
	RecoverPending(ctx, w.ledger, "whatsapp", func(sendCtx context.Context, userID, text string) error {
		return w.SendText(sendCtx, userID, text, "")
	})
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

// logSecurityWarnings surfaces exposure loudly at startup, never
// silently: an open sender list and a missing app_secret are the two
// ways this webhook answers the whole internet.
func (w *whatsapp) logSecurityWarnings() {
	if len(w.cfg.AllowedSenders) == 0 {
		log.Printf("WARNING: whatsapp allowed_senders is empty - the bot answers ANYONE who reaches the webhook; set allowed_senders in fiveagent.yml to restrict")
	}
	if w.cfg.AppSecret == "" {
		log.Printf("WARNING: whatsapp app_secret is not set - the webhook runs UNVERIFIED: anyone who reaches the URL can post fake messages; set app_secret in fiveagent.yml (env expansion works: app_secret: ${FIVEAGENT_META_APP_SECRET})")
	}
}

// validSignature checks Meta's X-Hub-Signature-256 header (HMAC-SHA256 of
// the raw body with the app secret).
func validSignature(secret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)
	got, err := hex.DecodeString(sig)
	if err != nil || len(got) != len(want) {
		return false
	}
	return hmac.Equal(got, want)
}

// verify answers Meta's webhook handshake.
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
				Messages []inboundMessage `json:"messages"`
				Statuses []struct {
					ID     string `json:"id"`
					Status string `json:"status"` // sent, delivered, read, failed
					Errors []struct {
						Title string `json:"title"`
					} `json:"errors"`
				} `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

// inboundMessage is one message in a webhook notification.
type inboundMessage struct {
	From string `json:"from"`
	ID   string `json:"id"`
	Type string `json:"type"`
	Text struct {
		Body string `json:"body"`
	} `json:"text"`
	Image    mediaRef `json:"image"`
	Audio    mediaRef `json:"audio"`
	Document mediaRef `json:"document"`
	Video    mediaRef `json:"video"`
	Sticker  mediaRef `json:"sticker"`
	Location *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
		Name      string  `json:"name"`
		Address   string  `json:"address"`
	} `json:"location"`
	Reaction *struct {
		MessageID string `json:"message_id"`
		Emoji     string `json:"emoji"`
	} `json:"reaction"`
	Context *struct {
		MessageID string `json:"id"` // quoted message wamid
	} `json:"context"`
}

// mediaRef is a reference to media stored on Meta's servers.
type mediaRef struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
}

// inbound receives notifications. Always 200 fast, process async (Meta
// retries on non-200). When app_secret is configured, unsigned or badly
// signed payloads are rejected with 401 instead.
func (w *whatsapp) inbound(rw http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		log.Printf("whatsapp: inbound POST unreadable: %v", err)
		rw.WriteHeader(http.StatusOK)
		return
	}
	if w.cfg.AppSecret != "" && !validSignature(w.cfg.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		log.Printf("whatsapp: rejected webhook with bad signature")
		http.Error(rw, "bad signature", http.StatusUnauthorized)
		return
	}
	rw.WriteHeader(http.StatusOK)

	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		log.Printf("whatsapp: bad payload (%d bytes): %v", len(body), err)
		return
	}
	log.Printf("whatsapp: inbound POST, %d bytes", len(body))
	for _, e := range p.Entry {
		for _, ch := range e.Changes {
			for _, s := range ch.Value.Statuses {
				if s.Status == "failed" {
					log.Printf("whatsapp: message %s FAILED: %+v", s.ID, s.Errors)
				} else {
					log.Printf("whatsapp: message %s -> %s", s.ID, s.Status)
				}
			}
			for _, m := range ch.Value.Messages {
				if !senderAllowed(w.cfg.AllowedSenders, m.From) {
					log.Printf("whatsapp: ignored message from %s (not in allowed_senders)", m.From)
					continue
				}
				if m.Type == "reaction" {
					// A reaction is feedback on a message, not a turn:
					// never run the agent for it (it used to answer with
					// a full model run). Structured capture is roadmap
					// stage f (learnings).
					if m.Reaction != nil {
						log.Printf("whatsapp: reaction %q to %s from %s (not a turn)", m.Reaction.Emoji, m.Reaction.MessageID, m.From)
					}
					continue
				}
				log.Printf("whatsapp: message from %s (type %s)", m.From, m.Type)
				w.enqueue(m.From, m.ID, describe(m), quoteOf(m))
			}
		}
	}
}

// describe turns one inbound message into text for the agent.
func describe(m inboundMessage) string {
	cap := func(s string) string {
		if s == "" {
			return ""
		}
		return ": " + s
	}
	switch m.Type {
	case "text":
		return m.Text.Body
	case "image":
		return "[image" + cap(m.Image.Caption) + "]"
	case "audio":
		return "[voice note]"
	case "document":
		return "[document: " + m.Document.Filename + cap(m.Document.Caption) + "]"
	case "video":
		return "[video" + cap(m.Video.Caption) + "]"
	case "sticker":
		return "[sticker]"
	case "location":
		if m.Location != nil {
			return fmt.Sprintf("[location: %f,%f %s %s]", m.Location.Latitude, m.Location.Longitude, m.Location.Name, m.Location.Address)
		}
		return "[location]"
	case "reaction":
		if m.Reaction != nil {
			return "[reaction " + m.Reaction.Emoji + " to " + m.Reaction.MessageID + "]"
		}
		return "[reaction]"
	default:
		return "[" + m.Type + " message - not supported yet]"
	}
}

func quoteOf(m inboundMessage) string {
	if m.Context != nil {
		return m.Context.MessageID
	}
	return ""
}

// process reacts to the message, marks it read + typing, runs the agent,
// sends the reply
// quoting the original message. Each phase gets its own fresh context:
// the agent's deadline must never kill the reply send (a slow model used
// to mean total silence for the user).
// enqueue feeds an inbound message into the per-sender debounce queue:
// messages arriving within the debounce window join one burst and are
// answered together; with debounce off every message is answered on
// arrival.
func (w *whatsapp) enqueue(from, msgID, text, replyTo string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if !w.debounceOn {
		go w.process(from, msgID, text, replyTo)
		return
	}
	w.mu.Lock()
	b := w.pending[from]
	if b == nil {
		b = &burst{}
		w.pending[from] = b
	}
	b.msgs = append(b.msgs, queuedMsg{msgID: msgID, text: text, replyTo: replyTo})
	if b.timer != nil {
		b.timer.Stop()
	}
	b.timer = time.AfterFunc(w.debounce, func() { w.flush(from) })
	w.mu.Unlock()
}

// flush closes one sender's burst and processes it as a single turn.
func (w *whatsapp) flush(from string) {
	w.mu.Lock()
	b := w.pending[from]
	delete(w.pending, from)
	w.mu.Unlock()
	if b == nil || len(b.msgs) == 0 {
		return
	}
	w.processBatch(from, b.msgs)
}

func (w *whatsapp) process(from, msgID, text, replyTo string) {
	w.processBatch(from, []queuedMsg{{msgID: msgID, text: text, replyTo: replyTo}})
}

// processBatch answers one sender turn: a single message or a debounced
// burst. The burst texts join in arrival order, the reply quotes the
// last message, and reactions cover every message: 👀 on the first,
// ✅/⚠️ on all when the turn ends.
func (w *whatsapp) processBatch(from string, msgs []queuedMsg) {
	react := func(msgID, emoji string) {
		if !w.reactions {
			return
		}
		rCtx, rCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rCancel()
		// A reaction failure is logged but never affects the reply.
		if err := w.React(rCtx, from, msgID, emoji); err != nil {
			log.Printf("whatsapp: react: %v", err)
		}
	}
	reactAll := func(emoji string) {
		for _, m := range msgs {
			react(m.msgID, emoji)
		}
	}
	react(msgs[0].msgID, "\U0001F440") // eyes: got it, working on it

	var texts []string
	for _, m := range msgs {
		texts = append(texts, m.text)
		mrCtx, mrCancel := context.WithTimeout(context.Background(), 15*time.Second)
		// A mark-read failure is logged but never affects the reply.
		if err := w.markRead(mrCtx, m.msgID); err != nil {
			log.Printf("whatsapp: mark read: %v", err)
		}
		mrCancel()
	}
	text := strings.Join(texts, "\n")
	replyTo := msgs[len(msgs)-1].replyTo

	budget := w.agentTimeout
	if budget <= 0 {
		budget = 5 * time.Minute
	}
	agentCtx, agentCancel := context.WithTimeout(context.Background(), budget)
	reply, err := w.core.Handle(agentCtx, "whatsapp", from, text)
	agentCancel()
	agentFailed := err != nil
	if agentFailed {
		log.Printf("whatsapp: agent: %v", err)
		if errors.Is(err, context.DeadlineExceeded) {
			reply = "El modelo está tardando demasiado. Inténtalo de nuevo."
		} else {
			reply = "Lo siento, algo ha fallado. Inténtalo de nuevo en un momento."
		}
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer sendCancel()
	// Stage 19: record the reply BEFORE sending; a crash after this
	// point redelivers it on the next start with the recovered marker.
	var d *Delivery
	if w.ledger != nil {
		if dd, lerr := w.ledger.Add("whatsapp", from, reply); lerr != nil {
			log.Printf("whatsapp: delivery ledger: %v", lerr)
		} else {
			d = dd
			w.ledger.Attempting(d.ID)
		}
	}
	sendErr := w.SendText(sendCtx, from, reply, replyTo)
	if sendErr != nil {
		log.Printf("whatsapp: send: %v", sendErr)
		if d != nil {
			w.ledger.Failed(d.ID, sendErr)
		}
		reactAll("⚠️")
		return
	}
	if d != nil {
		w.ledger.Delivered(d.ID)
	}
	if agentFailed {
		reactAll("⚠️") // the reply was a fallback, not a real answer
	} else {
		reactAll("✅")
	}
}

// React sets the agent's reaction on a message. WhatsApp keeps one
// reaction per user per message: a second reaction on the same message
// replaces the first, and an empty emoji removes it.
func (w *whatsapp) React(ctx context.Context, to, messageID, emoji string) error {
	return w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "reaction",
		"reaction":          map[string]string{"message_id": messageID, "emoji": emoji},
	})
}

// markRead marks a message as read and shows the typing indicator.
func (w *whatsapp) markRead(ctx context.Context, messageID string) error {
	return w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        messageID,
		"typing_indicator":  map[string]string{"type": "text"},
	})
}

// MountLinks serves the links handler (stage 24) on the same
// listener as the webhook, under /l/.
func (w *whatsapp) MountLinks(h http.Handler) {
	w.mux.Handle("/l/", h)
}

// Deliver sends an unquoted text, used by the scheduler (stage 22).
func (w *whatsapp) Deliver(ctx context.Context, userID, text string) error {
	return w.SendText(ctx, userID, text, "")
}

// SendText sends a text message. replyTo (a wamid) quotes that message.
func (w *whatsapp) SendText(ctx context.Context, to, text, replyToMessageID string) error {
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "text",
		"text":              map[string]string{"body": text},
	}
	if replyToMessageID != "" {
		payload["context"] = map[string]string{"message_id": replyToMessageID}
	}
	return w.post(ctx, payload)
}

// SendMedia sends an image, audio, document or video hosted at link,
// with an optional caption.
func (w *whatsapp) SendMedia(ctx context.Context, to, mediaType, link, caption string) error {
	body := map[string]string{"link": link}
	if caption != "" && (mediaType == "image" || mediaType == "document" || mediaType == "video") {
		body["caption"] = caption
	}
	return w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              mediaType,
		mediaType:           body,
	})
}

// SendTemplate sends an approved template (required outside the 24 h window).
func (w *whatsapp) SendTemplate(ctx context.Context, to, templateName, lang string, bodyParams []string) error {
	tmpl := map[string]any{
		"name":     templateName,
		"language": map[string]string{"code": lang},
	}
	if len(bodyParams) > 0 {
		params := make([]map[string]string, len(bodyParams))
		for i, p := range bodyParams {
			params[i] = map[string]string{"type": "text", "text": p}
		}
		tmpl["components"] = []map[string]any{{
			"type":       "body",
			"parameters": params,
		}}
	}
	return w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "template",
		"template":          tmpl,
	})
}

// UploadMedia uploads media bytes and returns the media id to send by id.
func (w *whatsapp) UploadMedia(ctx context.Context, mimeType, filename string, data []byte) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("messaging_product", "whatsapp")
	_ = mw.WriteField("type", mimeType)
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(data); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/%s/media", w.baseURL, w.cfg.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+w.cfg.AccessToken)
	resp, err := w.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("graph api: %s: %s", resp.Status, raw)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// DownloadMedia fetches inbound media bytes (e.g. a voice note) by media id.
func (w *whatsapp) DownloadMedia(ctx context.Context, mediaID string) ([]byte, string, error) {
	meta, err := w.graphGet(ctx, w.baseURL+"/"+mediaID)
	if err != nil {
		return nil, "", err
	}
	var info struct {
		URL      string `json:"url"`
		MimeType string `json:"mime_type"`
	}
	if err := json.Unmarshal(meta, &info); err != nil {
		return nil, "", err
	}
	data, err := w.graphGet(ctx, info.URL)
	if err != nil {
		return nil, "", err
	}
	return data, info.MimeType, nil
}

// post sends one payload to the messages endpoint.
func (w *whatsapp) post(ctx context.Context, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s/messages", w.baseURL, w.cfg.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.cfg.AccessToken)
	resp, err := w.http.Do(req)
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

// graphGet does an authenticated GET against the Graph API.
func (w *whatsapp) graphGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+w.cfg.AccessToken)
	resp, err := w.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("graph api: %s: %s", resp.Status, raw)
	}
	return raw, nil
}
