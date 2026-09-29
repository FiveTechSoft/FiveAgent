package agent

// Stage 7n battery: a conversation that mentions a fact (no
// "recuerda:" anywhere) recalls it later WITHOUT a single save_memory
// call - the fact landed through the indexer. And one sender's
// indexed facts never surface for another.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// fakeExtractor answers the extraction prompt with fixed facts.
type fakeExtractor struct {
	answer    string
	gotUser   string
	sawData   bool
	callCount int
}

func (f *fakeExtractor) Chat(ctx context.Context, msgs []model.Message, toolSpecs []tools.Spec) (model.Message, error) {
	f.callCount++
	for _, m := range msgs {
		if m.Role == "user" {
			f.gotUser = m.Content
			if strings.Contains(m.Content, "<exchange>") && strings.Contains(m.Content, "</exchange>") {
				f.sawData = true
			}
		}
	}
	return model.Message{Role: "assistant", Content: f.answer}, nil
}

func TestIndexerLandsFactWithoutSaveMemory(t *testing.T) {
	root := t.TempDir()
	// The global scope exists (as in production) but NOTHING is saved
	// to it - no save_memory call happens in this test at all.
	if _, err := memory.OpenKnowledge(root); err != nil {
		t.Fatal(err)
	}
	fx := &fakeExtractor{answer: "preferences: su comida favorita es el pulpo a la gallega"}
	ix := NewIndexer(fx, root)
	ix.Enqueue("whatsapp", "whatsapp/user-a", "te cuento que mi comida favorita es el pulpo a la gallega, apuntalo mentalmente", "que rico, el pulpo")
	ix.Drain(context.Background())
	if fx.callCount != 1 {
		t.Fatalf("extractor calls: %d", fx.callCount)
	}
	if !fx.sawData {
		t.Fatal("exchange must travel delimited as DATA")
	}
	// A later session (a fresh knowledge open on the same scope)
	// recalls the fact by a related query.
	later, err := memory.OpenKnowledge(filepath.Join(root, "users", "whatsapp_user-a"))
	if err != nil {
		t.Fatal(err)
	}
	hits, err := later.Recall("cual es su comida favorita")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(strings.Join(hits[0].Lines, " "), "pulpo") {
		t.Fatalf("fact not recalled: %+v", hits)
	}
	// The GLOBAL scope stays empty of the fact: it never went through
	// deliberate curation.
	global, _ := memory.OpenKnowledge(root)
	ghits, _ := global.Recall("pulpo")
	for _, h := range ghits {
		for _, ln := range h.Lines {
			if strings.Contains(ln, "pulpo a la gallega") {
				t.Fatalf("indexed fact leaked into the global scope: %s", ln)
			}
		}
	}
}

func TestIndexerScopesPerSender(t *testing.T) {
	root := t.TempDir()
	fx := &fakeExtractor{answer: "preferences: dato secreto del usuario A sobre vino"}
	ix := NewIndexer(fx, root)
	ix.Enqueue("whatsapp", "whatsapp/user-a", "me encanta el vino de la rioja alavesa, que conste", "anotado")
	ix.Drain(context.Background())
	// Sender B's scope: opened fresh, must NOT contain A's fact.
	bScope, err := memory.OpenKnowledge(filepath.Join(root, "users", "whatsapp_user-b"))
	if err != nil {
		t.Fatal(err)
	}
	hits, _ := bScope.Recall("vino")
	for _, h := range hits {
		for _, ln := range h.Lines {
			if strings.Contains(ln, "secreto del usuario A") {
				t.Fatalf("sender A fact surfaced for sender B: %s", ln)
			}
		}
	}
}

func TestIndexerSkipsTriviaAndNONE(t *testing.T) {
	root := t.TempDir()
	fx := &fakeExtractor{answer: "NONE"}
	ix := NewIndexer(fx, root)
	ix.Enqueue("whatsapp", "u1", "ok", "ok")
	ix.Enqueue("whatsapp", "u1", "hola", "hola, que tal")
	if len(ix.queue) != 0 {
		t.Fatal("trivia must be skipped by the pre-filter, never enqueued")
	}
	ix.Enqueue("whatsapp", "u1", "cuentame un chiste largo sobre programadores de harbour", "ahi va")
	ix.Drain(context.Background())
	if fx.callCount != 1 {
		t.Fatalf("only the non-trivial turn should reach the extractor: %d", fx.callCount)
	}
	// NONE wrote nothing: no user scope was even created.
	if _, err := ix.userScope("u1"); err == nil {
		if hits, _ := ix.users["u1"].Recall("chiste"); len(hits) > 0 {
			t.Fatal("NONE must write nothing")
		}
	}
}

func TestParseFactLineStrict(t *testing.T) {
	for _, bad := range []string{
		"no colon here",
		"evil: valid-looking fact but unknown file",
		"preferences: short",
		"NONE",
	} {
		if _, _, ok := parseFactLine(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	file, fact, ok := parseFactLine("people: - Maria es su hermana y vive en Vigo")
	if !ok || file != "people" || strings.HasPrefix(fact, "-") {
		t.Fatalf("good line misparsed: %q %q %v", file, fact, ok)
	}
}
