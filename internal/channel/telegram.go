package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
)

// telegram is the Telegram Bot API adapter. It uses long polling, so it
// works behind NAT with no public URL or tunnel: the agent calls out to
// Telegram and nothing needs to listen on the internet.
type telegram struct {
	cfg     config.Channel
	core    Handler
	http    *http.Client
	baseURL string // bot api base, overridable in tests
	// agentTimeout caps one full agent run; set by Build from the model
	// timeout, defaults to 5 minutes.
	agentTimeout time.Duration
	// ledger records outbound replies before sending (stage 19); nil
	// disables the ledger.
	ledger *Ledger
}

// NewTelegram builds the Telegram adapter.
func NewTelegram(cfg config.Channel, core Handler) Channel {
	return &telegram{
		cfg:     cfg,
		core:    core,
		http:    &http.Client{Timeout: 45 * time.Second}, // > poll timeout
		baseURL: "https://api.telegram.org",
	}
}

func (t *telegram) Name() string { return "telegram" }

// Run checks the token, then polls getUpdates until ctx is cancelled.
func (t *telegram) Run(ctx context.Context) error {
	if len(t.cfg.AllowedSenders) == 0 {
		log.Printf("WARNING: telegram allowed_senders is empty - the bot answers ANY chat that finds it; set allowed_senders in fiveagent.yml to restrict")
	}
	me, err := t.getMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram: token check failed: %w", err)
	}
	log.Printf("telegram: connected as @%s, long polling (no public URL needed)", me)

	// Stage 19: redeliver replies a crash left unsent, with marker.
	RecoverPending(ctx, t.ledger, "telegram", func(sendCtx context.Context, userID, text string) error {
		return t.SendText(sendCtx, userID, text, 0)
	})

	offset := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		updates, err := t.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("telegram: getUpdates: %v (retrying in 5s)", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			if u.Message != nil {
				go t.process(u.Message)
			}
		}
	}
}

// tgUpdate is the subset of a Telegram update we use.
type tgUpdate struct {
	UpdateID int        `json:"update_id"`
	Message  *tgMessage `json:"message"`
}

// tgMessage is one incoming message.
type tgMessage struct {
	MessageID int `json:"message_id"`
	From      struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
	} `json:"from"`
	Chat struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	Text    string `json:"text"`
	Caption string `json:"caption"`
	Photo   []struct {
		FileID string `json:"file_id"`
	} `json:"photo"`
	Document *struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
	} `json:"document"`
	Voice *struct {
		FileID   string `json:"file_id"`
		Duration int    `json:"duration"`
	} `json:"voice"`
	Video *struct {
		FileID string `json:"file_id"`
	} `json:"video"`
	Sticker *struct {
		Emoji string `json:"emoji"`
	} `json:"sticker"`
	Location *struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
}

// describe turns one Telegram message into text for the agent.
func (m *tgMessage) describe() string {
	cap := func(c string) string {
		if c == "" {
			return ""
		}
		return ": " + c
	}
	switch {
	case m.Text != "":
		return m.Text
	case len(m.Photo) > 0:
		return "[photo" + cap(m.Caption) + "]"
	case m.Document != nil:
		return "[document: " + m.Document.FileName + cap(m.Caption) + "]"
	case m.Voice != nil:
		return fmt.Sprintf("[voice message, %ds]", m.Voice.Duration)
	case m.Video != nil:
		return "[video" + cap(m.Caption) + "]"
	case m.Sticker != nil:
		return "[sticker " + m.Sticker.Emoji + "]"
	case m.Location != nil:
		return fmt.Sprintf("[location: %f,%f]", m.Location.Latitude, m.Location.Longitude)
	default:
		return "[unsupported message type]"
	}
}

// process runs the agent and replies, quoting the original message.
// Each phase gets its own fresh context: the agent's deadline must
// never kill the reply send.
func (t *telegram) process(m *tgMessage) {
	chatID := strconv.FormatInt(m.Chat.ID, 10)
	log.Printf("telegram: message from chat %s", chatID)
	if !senderAllowed(t.cfg.AllowedSenders, chatID) {
		log.Printf("telegram: ignored message from chat %s (not in allowed_senders)", chatID)
		return
	}
	text := m.describe()
	if strings.TrimSpace(text) == "" {
		return
	}
	taCtx, taCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := t.sendChatAction(taCtx, chatID); err != nil {
		log.Printf("telegram: typing indicator: %v", err)
	}
	taCancel()
	budget := t.agentTimeout
	if budget <= 0 {
		budget = 5 * time.Minute
	}
	agentCtx, agentCancel := context.WithTimeout(context.Background(), budget)
	reply, err := t.core.Handle(agentCtx, "telegram", chatID, text)
	agentCancel()
	if err != nil {
		log.Printf("telegram: agent: %v", err)
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
	if t.ledger != nil {
		if dd, lerr := t.ledger.Add("telegram", chatID, reply); lerr != nil {
			log.Printf("telegram: delivery ledger: %v", lerr)
		} else {
			d = dd
			t.ledger.Attempting(d.ID)
		}
	}
	sendErr := t.SendText(sendCtx, chatID, reply, m.MessageID)
	if sendErr != nil {
		log.Printf("telegram: send: %v", sendErr)
		if d != nil {
			t.ledger.Failed(d.ID, sendErr)
		}
	} else if d != nil {
		t.ledger.Delivered(d.ID)
	}
}

// SendText sends a text message. replyToMessageID > 0 quotes that message.
func (t *telegram) SendText(ctx context.Context, chatID, text string, replyToMessageID int) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if replyToMessageID > 0 {
		payload["reply_to_message_id"] = replyToMessageID
	}
	return t.call(ctx, "sendMessage", payload, nil)
}

// SendMedia sends a photo, document, audio or video hosted at url,
// with an optional caption.
func (t *telegram) SendMedia(ctx context.Context, chatID, kind, url, caption string) error {
	method := map[string]string{
		"photo": "sendPhoto", "document": "sendDocument",
		"audio": "sendAudio", "video": "sendVideo",
	}[kind]
	if method == "" {
		return fmt.Errorf("telegram: unknown media kind %q", kind)
	}
	payload := map[string]any{"chat_id": chatID, kind: url}
	if caption != "" {
		payload["caption"] = caption
	}
	return t.call(ctx, method, payload, nil)
}

// sendChatAction shows "typing..." in the chat.
func (t *telegram) sendChatAction(ctx context.Context, chatID string) error {
	return t.call(ctx, "sendChatAction", map[string]any{
		"chat_id": chatID, "action": "typing",
	}, nil)
}

// getMe returns the bot's username, proving the token works.
func (t *telegram) getMe(ctx context.Context) (string, error) {
	var out struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := t.call(ctx, "getMe", nil, &out); err != nil {
		return "", err
	}
	return out.Username, nil
}

// getUpdates long-polls for updates after offset.
func (t *telegram) getUpdates(ctx context.Context, offset int) ([]tgUpdate, error) {
	var out []tgUpdate
	err := t.call(ctx, "getUpdates", map[string]any{
		"offset": offset, "timeout": 30,
		"allowed_updates": []string{"message"},
	}, &out)
	return out, err
}

// call POSTs one Bot API method and decodes result into out.
func (t *telegram) call(ctx context.Context, method string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	url := fmt.Sprintf("%s/bot%s/%s", t.baseURL, t.cfg.BotToken, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var envelope struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("telegram api: bad response: %s", raw)
	}
	if !envelope.OK {
		return fmt.Errorf("telegram api: %s", envelope.Description)
	}
	if out != nil && len(envelope.Result) > 0 {
		return json.Unmarshal(envelope.Result, out)
	}
	return nil
}
