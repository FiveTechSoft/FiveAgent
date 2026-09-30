package memory

// Stage 7c: the FTS5 cache serves recall, rebuilds on drift, keeps up
// with writes, and its failure degrades to the keyword path - never to
// a wrong answer. ix.queries is the honest signal: a recall that
// silently fell back to keywords leaves it at zero and fails the test.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFTSIndexServesRecall(t *testing.T) {
	dir := t.TempDir()
	k, err := OpenKnowledge(dir)
	if err != nil {
		t.Fatal(err)
	}
	if k.ix == nil {
		t.Skip("no FTS5 in this SQLite build; keyword path is the only one here")
	}
	if _, err := k.Append("preferences", "su comida favorita es el pulpo a la gallega"); err != nil {
		t.Fatal(err)
	}
	hits, err := k.Recall("¿cuál es mi comida favorita?")
	if err != nil {
		t.Fatal(err)
	}
	if k.ix.queries == 0 {
		t.Fatal("recall did not use the FTS index")
	}
	found := false
	for _, h := range hits {
		for _, ln := range h.Lines {
			if strings.Contains(ln, "pulpo") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("FTS recall missed the fact: %+v", hits)
	}

	// Writes reindex at once: a second fact is findable right away.
	if _, err := k.Append("people", "mi hermano se llama Pelayo"); err != nil {
		t.Fatal(err)
	}
	hits, err = k.Recall("¿cómo se llama mi hermano?")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, h := range hits {
		for _, ln := range h.Lines {
			if strings.Contains(ln, "Pelayo") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("FTS recall missed a just-written fact: %+v", hits)
	}

	// Drift: a file edited behind the index's back is picked up on the
	// next open - the files are the source of truth, the index a cache.
	raw, _ := os.ReadFile(filepath.Join(dir, "preferences.md"))
	if err := os.WriteFile(filepath.Join(dir, "preferences.md"),
		[]byte(strings.TrimRight(string(raw), "\n")+"\n- mi postre favorito es la filloa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k2, err := OpenKnowledge(dir)
	if err != nil {
		t.Fatal(err)
	}
	hits, err = k2.Recall("¿cuál es mi postre favorito?")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, h := range hits {
		for _, ln := range h.Lines {
			if strings.Contains(ln, "filloa") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("drifted index was not rebuilt: %+v", hits)
	}

	// Fallback: a broken index never breaks recall - the keyword path
	// answers with the same fact.
	k2.ix.db.Close()
	hits, err = k2.Recall("¿cuál es mi comida favorita?")
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, h := range hits {
		for _, ln := range h.Lines {
			if strings.Contains(ln, "pulpo") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("keyword fallback missed the fact after index failure: %+v", hits)
	}
}
