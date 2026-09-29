package identity

import (
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUnlinkedResolvesToItself(t *testing.T) {
	s := open(t)
	c, linked := s.Resolve("whatsapp", "user-a")
	if linked || c != "whatsapp/user-a" {
		t.Fatalf("unlinked sender not separate by default: %s %v", c, linked)
	}
	if got := s.Linked("whatsapp", "user-a"); len(got) != 0 {
		t.Fatalf("unlinked sender has links: %v", got)
	}
}

func TestLinkFlowBothDirections(t *testing.T) {
	s := open(t)
	code, err := s.NewCode("whatsapp", "user-a")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := s.Redeem("telegram", "user-b", code)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "whatsapp/user-a" {
		t.Fatalf("canonical wrong: %s", canonical)
	}
	// The linked side resolves to the canonical...
	if c, linked := s.Resolve("telegram", "user-b"); !linked || c != "whatsapp/user-a" {
		t.Fatalf("redeemer not linked: %s %v", c, linked)
	}
	// ...and the primary resolves to itself as a linked identity.
	if c, linked := s.Resolve("whatsapp", "user-a"); !linked || c != "whatsapp/user-a" {
		t.Fatalf("primary not linked: %s %v", c, linked)
	}
	// Audit sees both ends.
	if got := s.Linked("telegram", "user-b"); len(got) != 2 {
		t.Fatalf("audit incomplete: %v", got)
	}
	// Single use: the same code cannot link anyone else.
	if _, err := s.Redeem("slack", "user-c", code); err == nil {
		t.Fatal("code redeemed twice")
	}
}

func TestChainResolvesToOneRoot(t *testing.T) {
	s := open(t)
	code1, _ := s.NewCode("whatsapp", "user-a")
	if _, err := s.Redeem("telegram", "user-b", code1); err != nil {
		t.Fatal(err)
	}
	// A code created by the LINKED side still points at the root.
	code2, _ := s.NewCode("telegram", "user-b")
	canonical, err := s.Redeem("slack", "user-c", code2)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "whatsapp/user-a" {
		t.Fatalf("chain did not resolve to the root: %s", canonical)
	}
	if c, _ := s.Resolve("slack", "user-c"); c != "whatsapp/user-a" {
		t.Fatalf("third channel not at root: %s", c)
	}
}

func TestCodeExpiryAndWrongCode(t *testing.T) {
	s := open(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	s.SetNow(func() time.Time { return now })
	code, _ := s.NewCode("whatsapp", "user-a")
	if _, err := s.Redeem("telegram", "user-b", "deadbeef"); err == nil {
		t.Fatal("wrong code accepted")
	}
	s.SetNow(func() time.Time { return now.Add(11 * time.Minute) })
	if _, err := s.Redeem("telegram", "user-b", code); err == nil {
		t.Fatal("expired code accepted")
	}
	// The expired redemption consumed nothing: still unlinked.
	if _, linked := s.Resolve("telegram", "user-b"); linked {
		t.Fatal("expired code left a link")
	}
}

func TestSelfRedeemRejected(t *testing.T) {
	s := open(t)
	code, _ := s.NewCode("whatsapp", "user-a")
	if _, err := s.Redeem("whatsapp", "user-a", code); err == nil {
		t.Fatal("self-redemption accepted")
	}
}

func TestUnlinkAndPersistence(t *testing.T) {
	s := open(t)
	code, _ := s.NewCode("whatsapp", "user-a")
	if _, err := s.Redeem("telegram", "user-b", code); err != nil {
		t.Fatal(err)
	}
	// Persistence: a reopened store keeps the link.
	s2, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if c, linked := s2.Resolve("telegram", "user-b"); !linked || c != "whatsapp/user-a" {
		t.Fatalf("link not persisted: %s %v", c, linked)
	}
	// Unlink the primary: both sides separate again.
	ok, err := s2.Unlink("whatsapp", "user-a")
	if err != nil || !ok {
		t.Fatalf("unlink: %v %v", ok, err)
	}
	if _, linked := s2.Resolve("telegram", "user-b"); linked {
		t.Fatal("unlink left the secondary linked")
	}
	if ok, _ := s2.Unlink("whatsapp", "user-a"); ok {
		t.Fatal("second unlink reported a change")
	}
}
