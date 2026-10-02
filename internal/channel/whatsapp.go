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
	"github.com/FiveTechSoft/FiveAgent/internal/media"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
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
	// media processors (stage 25): nil means that direction is not
	// configured and inbound media is announced without content.
	transcriber media.Transcriber
	describer   media.Describer
	// tts synthesizes outbound voice notes (stage 25d); nil keeps
	// replies as text.
	tts media.Synthesizer
	// video extracts frames+audio from inbound videos (stage 25c);
	// nil keeps the plain "[video]" announcement.
	video media.VideoExtractor
	// learningsRoot is the knowledge root for reaction feedback
	// (stage 7f): a 👎/👍/❤️ on a reply is recorded as a learning in
	// the SENDER's scope (users/<sender>/learnings.md), so one user's
	// feedback never surfaces for another. Empty disables the capture.
	learningsRoot string
	// sent maps the wamid of each reply we sent to its text, so an
	// inbound reaction can be attributed to the exact reply. Bounded:
	// oldest entries are dropped past 500.
	sentMu    sync.Mutex
	sent      map[string]string
	sentOrder []string
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
	if cfg.TranscriberURL != "" {
		w.transcriber = media.HTTPTranscriber{URL: cfg.TranscriberURL}
	}
	if cfg.DescriberURL != "" {
		w.describer = media.HTTPDescriber{URL: cfg.DescriberURL, Model: cfg.DescriberModel, APIKey: cfg.DescriberKey}
	}
	if cfg.TTSURL != "" {
		w.tts = media.HTTTSynthesizer{URL: cfg.TTSURL, Model: cfg.TTSModel, Voice: cfg.TTSVoice, APIKey: cfg.TTSKey}
	}
	if ff, err := media.NewFFmpeg(cfg.FFmpegPath); err == nil {
		w.video = ff
	} else {
		log.Printf("whatsapp: video analysis off: %v", err)
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
					// a full model run). 👎/👍/❤️ on a reply we sent are
					// recorded as a learning in the sender's scope
					// (stage 7f).
					if m.Reaction != nil {
						log.Printf("whatsapp: reaction %q to %s from %s (not a turn)", m.Reaction.Emoji, m.Reaction.MessageID, m.From)
						w.recordFeedback(m.From, m.Reaction.Emoji, m.Reaction.MessageID)
					}
					continue
				}
				log.Printf("whatsapp: message from %s (type %s)", m.From, m.Type)
				w.enqueue(m.From, m.ID, w.describeInbound(m), quoteOf(m))
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

// describeInbound turns one inbound message into text for the agent,
// downloading and processing media when a processor is configured.
// Voice notes become their transcript; images become a description.
// When the matching processor is not configured - or processing fails -
// the agent gets an honest bracket note and never a silent drop. Media
// bytes and transcripts are never logged (privacy); ids and byte
// counts only. Processing happens after the webhook has already
// returned 200, so a slow transcription service does not stall Meta.
func (w *whatsapp) describeInbound(m inboundMessage) string {
	switch m.Type {
	case "audio":
		if w.transcriber == nil || m.Audio.ID == "" {
			return "[voice note - transcription not configured]"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		data, mimeType, err := w.DownloadMedia(ctx, m.Audio.ID)
		if err != nil {
			log.Printf("whatsapp: media %s download failed: %v", m.Audio.ID, err)
			return "[voice note - download failed]"
		}
		log.Printf("whatsapp: voice note %s: %d bytes (%s)", m.Audio.ID, len(data), mimeType)
		text, err := w.transcriber.Transcribe(ctx, data, mimeType)
		if err != nil || text == "" {
			log.Printf("whatsapp: media %s transcription failed: %v", m.Audio.ID, err)
			return "[voice note - transcription failed]"
		}
		return "[voice note] " + text
	case "image":
		if w.describer == nil || m.Image.ID == "" {
			return describe(m)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		data, mimeType, err := w.DownloadMedia(ctx, m.Image.ID)
		if err != nil {
			log.Printf("whatsapp: media %s download failed: %v", m.Image.ID, err)
			return "[image - download failed]"
		}
		log.Printf("whatsapp: image %s: %d bytes (%s)", m.Image.ID, len(data), mimeType)
		desc, err := w.describer.Describe(ctx, data, mimeType, m.Image.Caption)
		if err != nil || desc == "" {
			log.Printf("whatsapp: media %s description failed: %v", m.Image.ID, err)
			return "[image - description failed]"
		}
		return "[image: " + desc + "]"
	case "video":
		if w.video == nil || m.Video.ID == "" {
			return describe(m)
		}
		if w.transcriber == nil && w.describer == nil {
			// Nothing could consume the analysis - say so without
			// downloading megabytes of video first.
			return "[video - analysis not configured]"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		data, _, err := w.DownloadMedia(ctx, m.Video.ID)
		if err != nil {
			log.Printf("whatsapp: media %s download failed: %v", m.Video.ID, err)
			return "[video - download failed]"
		}
		log.Printf("whatsapp: video %s: %d bytes", m.Video.ID, len(data))
		frames, audio, err := w.video.Extract(ctx, data, 3)
		if err != nil {
			log.Printf("whatsapp: video %s extraction failed: %v", m.Video.ID, err)
			return "[video - frame extraction failed]"
		}
		var parts []string
		if len(audio) > 0 && w.transcriber != nil {
			if text, terr := w.transcriber.Transcribe(ctx, audio, "audio/wav"); terr == nil && text != "" {
				parts = append(parts, "audio: "+text)
			}
		}
		if w.describer != nil {
			var descs []string
			for _, frame := range frames {
				if d, derr := w.describer.Describe(ctx, frame, "image/jpeg", m.Video.Caption); derr == nil && d != "" {
					descs = append(descs, d)
				}
			}
			if len(descs) > 0 {
				parts = append(parts, "frames: "+strings.Join(descs, " / "))
			}
		}
		if len(parts) == 0 {
			return "[video - analysis not configured]"
		}
		return "[video] " + strings.Join(parts, " | ")
	default:
		return describe(m)
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
	sendErr := w.sendReply(sendCtx, from, reply, replyTo, agentFailed)
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

// WithLearnings points the adapter at the knowledge root used for
// reaction feedback (stage 7f). The method name is asserted through an
// interface in main, so channels without the concept need nothing.
func (w *whatsapp) WithLearnings(root string) { w.learningsRoot = root }

// rememberSent records the wamid->text mapping of one outbound reply so
// a later reaction can be attributed to it (stage 7f).
func (w *whatsapp) rememberSent(id, text string) {
	if id == "" {
		return
	}
	w.sentMu.Lock()
	defer w.sentMu.Unlock()
	if w.sent == nil {
		w.sent = map[string]string{}
	}
	if _, dup := w.sent[id]; !dup {
		w.sentOrder = append(w.sentOrder, id)
	}
	w.sent[id] = text
	for len(w.sentOrder) > 500 {
		delete(w.sent, w.sentOrder[0])
		w.sentOrder = w.sentOrder[1:]
	}
}

// recordFeedback turns an inbound reaction into a learning (stage 7f):
// 👎 is negative feedback, 👍/❤️ positive, on the exact reply the
// reaction points at. Other emojis and reactions to messages we did not
// send are not feedback we can attribute, so they are only logged. The
// note lands in the sender's own memory scope, never the global one.
func (w *whatsapp) recordFeedback(from, emoji, messageID string) {
	var kind string
	switch emoji {
	case "👎":
		kind = "negativo"
	case "👍", "❤️", "❤":
		kind = "positivo"
	default:
		return
	}
	if w.learningsRoot == "" {
		return
	}
	w.sentMu.Lock()
	excerpt, ok := w.sent[messageID]
	w.sentMu.Unlock()
	if !ok {
		log.Printf("whatsapp: reaction %q to unknown message %s - no feedback recorded", emoji, messageID)
		return
	}
	excerpt = strings.Join(strings.Fields(excerpt), " ")
	if len(excerpt) > 120 {
		excerpt = excerpt[:120] + "…"
	}
	kn, err := memory.OpenUserScope(w.learningsRoot, from)
	if err != nil {
		log.Printf("whatsapp: reaction feedback scope: %v", err)
		return
	}
	entry := fmt.Sprintf("El usuario marcó mi respuesta como %s (%s): %q",
		kind, time.Now().Format("2006-01-02"), excerpt)
	if _, err := kn.AppendFrom("learnings", entry, "reaction feedback"); err != nil {
		log.Printf("whatsapp: reaction feedback: %v", err)
		return
	}
	log.Printf("whatsapp: reaction feedback recorded (%s) for %s", kind, from)
}

// React sets the agent's reaction on a message. WhatsApp keeps one
// reaction per user per message: a second reaction on the same message
// replaces the first, and an empty emoji removes it.
func (w *whatsapp) React(ctx context.Context, to, messageID, emoji string) error {
	_, err := w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                to,
		"type":              "reaction",
		"reaction":          map[string]string{"message_id": messageID, "emoji": emoji},
	})
	return err
}

// markRead marks a message as read and shows the typing indicator.
func (w *whatsapp) markRead(ctx context.Context, messageID string) error {
	_, err := w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"status":            "read",
		"message_id":        messageID,
		"typing_indicator":  map[string]string{"type": "text"},
	})
	return err
}

// MountLinks serves the links handler (stage 24) on the same
// listener as the webhook, under /l/.
func (w *whatsapp) MountLinks(h http.Handler) {
	w.mux.Handle("/l/", h)
}

// MountOAuth serves the OAuth connect handler (stage 27) on the same
// listener as the webhook, under /oauth/.
func (w *whatsapp) MountOAuth(h http.Handler) {
	w.mux.Handle("/oauth/", h)
}

// Deliver sends an unquoted text, used by the scheduler (stage 22)
// and proactive subscriptions (stage 35). It goes through the
// delivery ledger (stage 19) like the reply path: the obligation is
// recorded BEFORE the send, so a crash redelivers it on the next
// start with the recovered marker.
func (w *whatsapp) Deliver(ctx context.Context, userID, text string) error {
	var d *Delivery
	if w.ledger != nil {
		if dd, lerr := w.ledger.Add("whatsapp", userID, text); lerr != nil {
			log.Printf("whatsapp: delivery ledger: %v", lerr)
		} else {
			d = dd
			w.ledger.Attempting(d.ID)
		}
	}
	err := w.SendText(ctx, userID, text, "")
	if err != nil {
		if d != nil {
			w.ledger.Failed(d.ID, err)
		}
		return err
	}
	if d != nil {
		w.ledger.Delivered(d.ID)
	}
	return nil
}

// sendReply delivers the agent's reply: as a voice note when TTS is
// configured and the reply is a real answer, as text otherwise. Voice
// degrades honestly: any synthesis or upload failure falls back to the
// text reply (logged without content), so the user always gets the
// answer. Fallback error strings always go out as text - a synthetic
// voice apologizing would bury the signal that the agent failed.
func (w *whatsapp) sendReply(ctx context.Context, to, text, replyTo string, agentFailed bool) error {
	if w.tts == nil || agentFailed {
		return w.SendText(ctx, to, text, replyTo)
	}
	ttsCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	audio, mimeType, err := w.tts.Synthesize(ttsCtx, text)
	if err != nil {
		log.Printf("whatsapp: tts failed (%v) - falling back to text", err)
		return w.SendText(ctx, to, text, replyTo)
	}
	log.Printf("whatsapp: tts reply: %d bytes (%s)", len(audio), mimeType)
	id, err := w.UploadMedia(ttsCtx, mimeType, "reply"+extForMIME(mimeType), audio)
	if err != nil {
		log.Printf("whatsapp: voice upload failed (%v) - falling back to text", err)
		return w.SendText(ctx, to, text, replyTo)
	}
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "audio",
		"audio":             map[string]any{"id": id},
	}
	if replyTo != "" {
		payload["context"] = map[string]string{"message_id": replyTo}
	}
	msgID, err := w.post(ctx, payload)
	if err != nil {
		log.Printf("whatsapp: voice send failed (%v) - falling back to text", err)
		return w.SendText(ctx, to, text, replyTo)
	}
	w.rememberSent(msgID, text)
	return nil
}

// extForMIME maps outbound media MIME types to a file extension for
// the upload filename.
func extForMIME(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "audio/ogg"), strings.HasPrefix(mimeType, "audio/opus"):
		return ".ogg"
	case strings.HasPrefix(mimeType, "audio/mpeg"):
		return ".mp3"
	case mimeType == "image/png":
		return ".png"
	case mimeType == "image/jpeg":
		return ".jpg"
	case mimeType == "video/mp4":
		return ".mp4"
	}
	return ".bin"
}

// SendText sends a text message. replyTo (a wamid) quotes that message.
// A successful send is remembered (stage 7f) so a later reaction on it
// can be attributed to this exact reply.
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
	id, err := w.post(ctx, payload)
	if err != nil {
		return err
	}
	w.rememberSent(id, text)
	return nil
}

// SendMedia sends an image, audio, document or video hosted at link,
// with an optional caption.
func (w *whatsapp) SendMedia(ctx context.Context, to, mediaType, link, caption string) error {
	body := map[string]string{"link": link}
	if caption != "" && (mediaType == "image" || mediaType == "document" || mediaType == "video") {
		body["caption"] = caption
	}
	_, err := w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              mediaType,
		mediaType:           body,
	})
	return err
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
	_, err := w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "template",
		"template":          tmpl,
	})
	return err
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

// SendMediaBytes uploads raw media bytes and sends them by id, with an
// optional caption (supported for images, documents and video). Stage
// 25e: this is how code-made artifacts (charts) reach the chat.
func (w *whatsapp) SendMediaBytes(ctx context.Context, to, mimeType, caption string, data []byte) error {
	kind := "document"
	switch {
	case mimeType == "image/png" || mimeType == "image/jpeg":
		kind = "image"
	case mimeType == "audio/ogg" || mimeType == "audio/mpeg":
		kind = "audio"
	case mimeType == "video/mp4":
		kind = "video"
	}
	id, err := w.UploadMedia(ctx, mimeType, "fiveagent"+extForMIME(mimeType), data)
	if err != nil {
		return err
	}
	body := map[string]any{"id": id}
	if caption != "" && kind != "audio" {
		body["caption"] = caption
	}
	_, err = w.post(ctx, map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              kind,
		kind:                body,
	})
	return err
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
// post sends one payload to /messages and returns the wamid the Graph
// API assigned ("" when the response carries none, e.g. mark-read).
func (w *whatsapp) post(ctx context.Context, payload map[string]any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/%s/messages", w.baseURL, w.cfg.PhoneNumberID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
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
	var ack struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &ack); err == nil && len(ack.Messages) > 0 {
		return ack.Messages[0].ID, nil
	}
	return "", nil
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
