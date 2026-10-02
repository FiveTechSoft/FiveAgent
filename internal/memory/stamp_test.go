package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedClock(k *Knowledge, day string) {
	d, _ := time.Parse("2006-01-02", day)
	k.now = func() time.Time { return d }
}

func TestAppendStampsDateAndOrigin(t *testing.T) {
	k := openTemp(t)
	fixedClock(k, "2026-10-02")
	if added, err := k.AppendFrom("people", "Ana es mi hermana", "save_memory"); err != nil || !added {
		t.Fatalf("append: %v %v", added, err)
	}
	raw, _ := os.ReadFile(filepath.Join(k.Dir(), "people.md"))
	if !strings.Contains(string(raw), "- Ana es mi hermana [2026-10-02, origin: save_memory]\n") {
		t.Fatalf("stamp missing or wrong:\n%s", raw)
	}
	// Plain Append labels the origin generically, never leaves it empty.
	if _, err := k.Append("people", "Luis es mi primo"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(k.Dir(), "people.md"))
	if !strings.Contains(string(raw), "Luis es mi primo [2026-10-02, origin: agent]") {
		t.Fatalf("default origin missing:\n%s", raw)
	}
}

func TestStampDoesNotBreakDedupOrDoubleStamp(t *testing.T) {
	k := openTemp(t)
	fixedClock(k, "2026-10-02")
	if _, err := k.AppendFrom("people", "Ana es mi hermana", "save_memory"); err != nil {
		t.Fatal(err)
	}
	// Same fact on a later day and from another origin is still a duplicate.
	fixedClock(k, "2026-10-09")
	added, err := k.AppendFrom("people", "ana es mi hermana.", "indexer")
	if err != nil || added {
		t.Fatalf("duplicate across days/origins must not be added: %v %v", added, err)
	}
	// An entry that already carries a stamp keeps exactly one.
	if _, err := k.AppendFrom("people", "Pau vive en Lyon [2026-01-05, origin: import]", "save_memory"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(k.Dir(), "people.md"))
	if strings.Count(string(raw), "Pau vive en Lyon") != 1 || strings.Count(string(raw), "origin:") != 2 {
		t.Fatalf("stamp duplicated or missing:\n%s", raw)
	}
}

func TestOriginCannotBreakTheStamp(t *testing.T) {
	k := openTemp(t)
	fixedClock(k, "2026-10-02")
	if _, err := k.AppendFrom("people", "Eva es mi vecina", "x]\n- injected line"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(k.Dir(), "people.md"))
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "- injected") {
			t.Fatalf("origin injected a bullet:\n%s", raw)
		}
	}
}

// Absent fact with a look-alike trap: recall must return nothing for a
// question about something never stored, and must not surface the
// look-alike as if it answered. This measures retrieval only; how the
// model words "not found" is a separate, still unmeasured behavior.
func TestRecallAbsentFactReturnsNothingNotTheLookAlike(t *testing.T) {
	k := openTemp(t)
	for _, n := range []string{"Mi perro se llama Tobi", "Mi perrera favorita está en Getafe", "Mi hermana Ana vive en Lyon"} {
		if _, err := k.Append("people", n); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := k.Recall("matrícula del coche de Ana")
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		for _, l := range h.Lines {
			if strings.Contains(strings.ToLower(l), "matrícula") || strings.Contains(strings.ToLower(l), "coche") {
				t.Fatalf("recall invented a match: %q", l)
			}
		}
	}
	// The stored fact is still found: the test must fail if recall is just empty.
	hits, err = k.Recall("perro Tobi")
	if err != nil || len(hits) == 0 || !strings.Contains(strings.Join(hits[0].Lines, " "), "Tobi") {
		t.Fatalf("stored fact not recalled: %v %+v", err, hits)
	}
}
