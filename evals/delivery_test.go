package evals

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/channel"
)

// Stage 19: durable delivery ledger. The crash fixture: a reply left
// mid-attempt when the process dies must be redelivered exactly once,
// with the recovered marker; capped and stale entries must not.

// TestDeliveryRedeliversOnceAfterCrash kills a send mid-flight: the
// ledger holds the reply in "attempting" when the process drops. The
// next start must redeliver it exactly once, with the marker.
func TestDeliveryRedeliversOnceAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deliveries.json")

	l1, err := channel.OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := l1.Add("testchan", "user1", "respuesta original")
	if err != nil {
		t.Fatal(err)
	}
	l1.Attempting(d.ID)
	// Crash: the process dies here, mid-send. l1 is simply dropped.

	l2, err := channel.OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	var sent []string
	send := func(ctx context.Context, userID, text string) error {
		sent = append(sent, userID+"|"+text)
		return nil
	}
	channel.RecoverPending(context.Background(), l2, "testchan", send)

	if len(sent) != 1 {
		t.Fatalf("expected exactly 1 redelivery, got %d: %v", len(sent), sent)
	}
	want := "user1|" + channel.RecoveredMarker + "respuesta original"
	if sent[0] != want {
		t.Fatalf("redelivery must carry the recovered marker:\n got %q\nwant %q", sent[0], want)
	}

	// A second sweep must NOT resend: delivered is final.
	channel.RecoverPending(context.Background(), l2, "testchan", send)
	if len(sent) != 1 {
		t.Fatalf("delivered reply resent on a second sweep: %v", sent)
	}

	// The file itself records the delivered state (durable, not RAM).
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"delivered"`) {
		t.Fatalf("ledger file must record the delivered state, got: %s", b)
	}
}

// TestDeliveryAttemptsCapped: an entry that exhausted its attempts is
// dead and the recovery sweep never retries it.
func TestDeliveryAttemptsCapped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deliveries.json")
	l, err := channel.OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := l.Add("testchan", "user1", "nunca llegará")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ { // cap is 5; 8 failed attempts must kill it
		l.Attempting(d.ID)
		l.Failed(d.ID, errors.New("boom"))
	}
	sent := 0
	send := func(ctx context.Context, userID, text string) error {
		sent++
		return nil
	}
	channel.RecoverPending(context.Background(), l, "testchan", send)
	if sent != 0 {
		t.Fatalf("dead entry retried %d times by the recovery sweep", sent)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"dead"`) {
		t.Fatalf("ledger file must record the dead state, got: %s", b)
	}
}

// TestDeliveryStaleExpires: a reply left undelivered for more than 24 h
// (crash fixture written directly) expires unsent instead of arriving
// confusingly late.
func TestDeliveryStaleExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deliveries.json")
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	fixture := `[{"id":"old1","channel":"testchan","user_id":"user1","text":"noticia de ayer","state":"pending","attempts":0,"created_at":"` + old + `"}]`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := channel.OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	sent := 0
	send := func(ctx context.Context, userID, text string) error {
		sent++
		return nil
	}
	channel.RecoverPending(context.Background(), l, "testchan", send)
	if sent != 0 {
		t.Fatalf("stale entry sent %d times; stale entries must expire unsent", sent)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "old1") {
		t.Fatalf("expired entry must be dropped from the ledger, got: %s", b)
	}
}

// TestDeliveryRecordedBeforeSend: the obligation exists on disk BEFORE
// the send is attempted - the property the whole stage exists for.
func TestDeliveryRecordedBeforeSend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deliveries.json")
	l, err := channel.OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := l.Add("testchan", "user1", "pendiente")
	if err != nil {
		t.Fatal(err)
	}
	// Before any Attempting/Delivered call, a fresh open must already
	// owe the reply.
	l2, err := channel.OpenLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	owed := l2.Pending("testchan", time.Now())
	if len(owed) != 1 || owed[0].ID != d.ID {
		t.Fatalf("reply not durable before send: owed=%v", owed)
	}
}
