package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Durable delivery ledger (stage 19). Every outbound reply is recorded
// as a persistent obligation BEFORE the send is attempted, so a crash
// between generating and sending never silently loses a reply: on the
// next start, pending entries are redelivered once, prefixed with
// RecoveredMarker. Attempts are capped and stale entries expire.

// Delivery states.
const (
	DeliveryPending    = "pending"
	DeliveryAttempting = "attempting"
	DeliveryDelivered  = "delivered"
	DeliveryDead       = "dead" // attempts exhausted; kept for diagnosis
)

const (
	// maxDeliveryAttempts caps send attempts per reply (the first try
	// plus crash recoveries); then the entry is dead and not retried.
	maxDeliveryAttempts = 5
	// maxDeliveryAge expires undelivered entries: a reply that arrives
	// a day late confuses more than it helps.
	maxDeliveryAge = 24 * time.Hour
)

// RecoveredMarker prefixes a reply redelivered after a crash, so the
// user can tell it apart from a live answer. A crash can land after
// the platform accepted the message but before the ledger recorded it;
// the resend is exactly why the marker exists.
const RecoveredMarker = "🔁 (recuperado) "

// Delivery is one outbound reply obligation.
type Delivery struct {
	ID        string    `json:"id"`
	Channel   string    `json:"channel"`
	UserID    string    `json:"user_id"`
	Text      string    `json:"text"`
	State     string    `json:"state"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
	LastError string    `json:"last_error,omitempty"`
}

// Ledger is a JSON-file-backed set of deliveries, shared by the
// channel adapters. Open with OpenLedger; concurrent-safe.
type Ledger struct {
	mu   sync.Mutex
	path string
	ds   []*Delivery
}

// OpenLedger loads the ledger at path, starting empty on first use. A
// crash can leave entries mid-attempt: those load back as pending so
// the recovery sweep retries them. Delivered entries older than
// maxDeliveryAge are pruned to keep the file small.
func OpenLedger(path string) (*Ledger, error) {
	l := &Ledger{path: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return l, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return l, nil
	}
	if err := json.Unmarshal(b, &l.ds); err != nil {
		return nil, fmt.Errorf("delivery ledger %s: %w", path, err)
	}
	cutoff := time.Now().Add(-maxDeliveryAge)
	kept := l.ds[:0]
	for _, d := range l.ds {
		if d.State == DeliveryAttempting {
			d.State = DeliveryPending // crashed mid-attempt
		}
		if d.State == DeliveryDelivered && d.CreatedAt.Before(cutoff) {
			continue // prune old history
		}
		kept = append(kept, d)
	}
	l.ds = kept
	return l, nil
}

// Add records a new outbound reply as pending and persists it BEFORE
// any send is attempted.
func (l *Ledger) Add(ch, userID, text string) (*Delivery, error) {
	var rnd [4]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	d := &Delivery{
		ID:        fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(rnd[:])),
		Channel:   ch,
		UserID:    userID,
		Text:      text,
		State:     DeliveryPending,
		CreatedAt: time.Now(),
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ds = append(l.ds, d)
	if err := l.saveLocked(); err != nil {
		return nil, err
	}
	return d, nil
}

// Attempting marks the entry as being sent right now.
func (l *Ledger) Attempting(id string) { l.set(id, DeliveryAttempting, "", false) }

// Delivered marks the entry as confirmed sent; delivered is final.
func (l *Ledger) Delivered(id string) { l.set(id, DeliveryDelivered, "", false) }

// Failed records a failed attempt and returns the entry to pending,
// unless the attempt cap is reached: then it is dead and not retried.
func (l *Ledger) Failed(id string, err error) { l.set(id, DeliveryPending, err.Error(), true) }

func (l *Ledger) set(id, state, lastErr string, incAttempt bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, d := range l.ds {
		if d.ID != id {
			continue
		}
		if incAttempt {
			d.Attempts++
		}
		if lastErr != "" {
			d.LastError = lastErr
		}
		d.State = state
		if d.State == DeliveryPending && d.Attempts >= maxDeliveryAttempts {
			d.State = DeliveryDead
			log.Printf("delivery %s: giving up after %d attempts", d.ID, d.Attempts)
		}
		break
	}
	if err := l.saveLocked(); err != nil {
		log.Printf("delivery ledger: save: %v", err)
	}
}

// Pending returns copies of the undelivered entries for ch that are
// still owed: pending, under the attempt cap, and not expired. Dead
// entries are never returned. Pending entries older than maxDeliveryAge
// expire: they are dropped from the ledger and logged, never sent.
func (l *Ledger) Pending(ch string, now time.Time) []Delivery {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Delivery
	kept := l.ds[:0]
	dirty := false
	for _, d := range l.ds {
		switch {
		case d.State == DeliveryPending && d.Attempts >= maxDeliveryAttempts:
			d.State = DeliveryDead // cap reached before a crash
			dirty = true
		case d.State == DeliveryPending && now.Sub(d.CreatedAt) > maxDeliveryAge:
			log.Printf("delivery %s: expired undelivered after %s", d.ID, now.Sub(d.CreatedAt).Round(time.Minute))
			dirty = true
			continue // expired: drop
		}
		kept = append(kept, d)
		if d.Channel == ch && d.State == DeliveryPending {
			out = append(out, *d)
		}
	}
	l.ds = kept
	if dirty {
		if err := l.saveLocked(); err != nil {
			log.Printf("delivery ledger: save: %v", err)
		}
	}
	return out
}

// RecoverPending redelivers every pending reply for ch exactly once,
// prefixed with RecoveredMarker; send performs one platform send. A
// failed resend goes back to pending (attempts capped by Failed) for
// the next start. Delivered entries are never resent.
func RecoverPending(ctx context.Context, l *Ledger, ch string, send func(ctx context.Context, userID, text string) error) {
	if l == nil {
		return
	}
	for _, d := range l.Pending(ch, time.Now()) {
		l.Attempting(d.ID)
		sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := send(sendCtx, d.UserID, RecoveredMarker+d.Text)
		cancel()
		if err != nil {
			log.Printf("delivery %s: redelivery failed: %v", d.ID, err)
			l.Failed(d.ID, err)
			continue
		}
		l.Delivered(d.ID)
		log.Printf("delivery %s: redelivered with recovered marker", d.ID)
	}
}

// saveLocked writes the ledger atomically (temp file + rename).
func (l *Ledger) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(l.ds, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}
