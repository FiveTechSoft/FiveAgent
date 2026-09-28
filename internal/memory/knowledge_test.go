package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
)

func openTemp(t *testing.T) *Knowledge {
	t.Helper()
	k, err := OpenKnowledge(filepath.Join(t.TempDir(), "memory"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func commitCount(t *testing.T, k *Knowledge) int {
	t.Helper()
	it, err := k.repo.Log(&git.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	defer it.Close()
	for {
		if _, err := it.Next(); err != nil {
			break
		}
		n++
	}
	return n
}

func TestOpenKnowledgeCreatesDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "memory")
	k, err := OpenKnowledge(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"people.md", "preferences.md", "workstreams.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing default file %s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Error("memory folder is not a git repo")
	}
	if n := commitCount(t, k); n != 1 {
		t.Errorf("commits after init = %d, want 1", n)
	}
	// Reopening keeps files and history, no duplicate init commit.
	k2, err := OpenKnowledge(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := commitCount(t, k2); n != 1 {
		t.Errorf("commits after reopen = %d, want 1", n)
	}
}

func TestAppendCommits(t *testing.T) {
	k := openTemp(t)
	before := commitCount(t, k)
	if _, err := k.Append("preferences", "Loves pasta; noted 2026-09-28."); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(k.dir, "preferences.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "- Loves pasta; noted 2026-09-28.") {
		t.Errorf("entry not appended:\n%s", raw)
	}
	if n := commitCount(t, k); n != before+1 {
		t.Errorf("commits = %d, want %d (one commit per append)", n, before+1)
	}
}

func TestAppendUnknownID(t *testing.T) {
	k := openTemp(t)
	_, err := k.Append("spaceships", "x")
	if err == nil || !strings.Contains(err.Error(), "people") {
		t.Errorf("want error naming available files, got: %v", err)
	}
}

func TestRecallByKeyword(t *testing.T) {
	k := openTemp(t)
	if _, err := k.Append("preferences", "Loves pasta; noted 2026-09-28."); err != nil {
		t.Fatal(err)
	}
	hits, err := k.Recall("¿le gusta la pasta?")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "preferences" {
		t.Fatalf("hits = %+v, want one hit in preferences", hits)
	}
	found := false
	for _, ln := range hits[0].Lines {
		if strings.Contains(ln, "Loves pasta") {
			found = true
		}
	}
	if !found {
		t.Errorf("hit lines lack the pasta entry: %v", hits[0].Lines)
	}
}

func TestRecallByAlias(t *testing.T) {
	k := openTemp(t)
	if _, err := k.Append("people", "María is his sister."); err != nil {
		t.Fatal(err)
	}
	// "contactos" is an alias of people.md, absent from its body.
	hits, err := k.Recall("¿qué contactos tengo?")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != "people" {
		t.Fatalf("hits = %+v, want people first", hits)
	}
	found := false
	for _, ln := range hits[0].Lines {
		if strings.Contains(ln, "María") {
			found = true
		}
	}
	if !found {
		t.Errorf("alias-only hit should return the body; lines: %v", hits[0].Lines)
	}
}

// TestRecallFindsDroppedFile verifies the documented import flow for
// training with a commercial AI: a markdown file dropped into the
// knowledge folder while the process is running (exported from
// ChatPT/Claude/Gemini, saved as ia-comercial.md) is picked up by the
// next recall without reopening the store.
func TestRecallFindsDroppedFile(t *testing.T) {
	k := openTemp(t)
	const file = "---\naliases: [openai, chatgpt, anthropic, claude]\n---\n" +
		"# IA comercial\n" +
		"- OpenAI desarrolla ChatGPT, lanzado al público en 2022.\n"
	if err := os.WriteFile(filepath.Join(k.dir, "ia-comercial.md"), []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	hits, err := k.Recall("¿qué es ChatGPT y quién lo hizo?")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.ID != "ia-comercial" {
			continue
		}
		for _, ln := range h.Lines {
			if strings.Contains(ln, "ChatGPT") {
				return
			}
		}
		t.Fatalf("ia-comercial hit lacks the ChatGPT bullet: %v", h.Lines)
	}
	t.Fatalf("dropped file ia-comercial.md not recalled; hits: %+v", hits)
}

func TestRecallNoMatch(t *testing.T) {
	k := openTemp(t)
	hits, err := k.Recall("xyzzy quantum")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("hits = %+v, want none", hits)
	}
}

func TestRecallRanksRelevance(t *testing.T) {
	k := openTemp(t)
	if _, err := k.Append("preferences", "Loves pasta and paella."); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Append("workstreams", "Paused the pasta blog redesign."); err != nil {
		t.Fatal(err)
	}
	hits, err := k.Recall("pasta paella")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "preferences" {
		t.Errorf("hits = %+v, want preferences first (2 line matches)", hits)
	}
}

func TestAppendDeduplicates(t *testing.T) {
	k := openTemp(t)
	if added, err := k.Append("preferences", "Loves pasta."); err != nil || !added {
		t.Fatalf("first append: added=%v err=%v", added, err)
	}
	// Same fact, different casing, spacing and trailing period: no copy.
	if added, err := k.Append("preferences", "  loves pasta"); err != nil || added {
		t.Fatalf("duplicate append: added=%v err=%v, want added=false", added, err)
	}
	raw, _ := os.ReadFile(filepath.Join(k.dir, "preferences.md"))
	if n := strings.Count(strings.ToLower(string(raw)), "loves pasta"); n != 1 {
		t.Errorf("file holds %d copies, want 1", n)
	}
}

func TestForget(t *testing.T) {
	k := openTemp(t)
	if _, err := k.Append("preferences", "Loves pasta."); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Append("preferences", "Hates cilantro."); err != nil {
		t.Fatal(err)
	}
	n, err := k.Forget("preferences", "PASTA")
	if err != nil || n != 1 {
		t.Fatalf("forget: n=%d err=%v, want n=1", n, err)
	}
	raw, _ := os.ReadFile(filepath.Join(k.dir, "preferences.md"))
	if strings.Contains(string(raw), "pasta") {
		t.Error("pasta entry not removed")
	}
	if !strings.Contains(string(raw), "cilantro") {
		t.Error("unrelated entry removed")
	}
	if !strings.HasPrefix(string(raw), "---\nid: preferences") {
		t.Error("header damaged")
	}
	// Forgetting something absent is a no-op with no commit.
	before := commitCount(t, k)
	if n, err := k.Forget("preferences", "sushi"); err != nil || n != 0 {
		t.Fatalf("forget absent: n=%d err=%v", n, err)
	}
	if c := commitCount(t, k); c != before {
		t.Errorf("no-op forget committed: %d -> %d", before, c)
	}
}
