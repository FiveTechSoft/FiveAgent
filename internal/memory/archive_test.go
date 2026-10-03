package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stampEntry writes a memory file with explicit stamp dates (the test
// cannot travel in time: Append stamps with k.now, but aging needs
// dates only the file content can fake).
func writeStamped(t *testing.T, path, id, old, fresh string) {
	t.Helper()
	raw := "---\nid: " + id + "\n---\n# " + id + "\n" +
		"- Come pulpo a la gallega los domingos [" + old + ", origin: save_memory]\n" +
		"- Prefiere el verde musgo [" + fresh + ", origin: save_memory]\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestArchiveStaleAgesEntriesOutOfRecall: a stamped entry older than
// the cutoff moves to archive/<file>.md, leaves recall (top-level
// files only), keeps the recent entry and the header, and is
// idempotent.
func TestArchiveStaleAgesEntriesOutOfRecall(t *testing.T) {
	k := openTemp(t)
	old := time.Now().AddDate(0, 0, -60).Format("2006-01-02")
	fresh := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	writeStamped(t, filepath.Join(k.dir, "preferences.md"), "preferences", old, fresh)

	n, err := k.ArchiveStale(30, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("archive: n=%d err=%v, want n=1", n, err)
	}

	src, err := os.ReadFile(filepath.Join(k.dir, "preferences.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "pulpo") {
		t.Error("stale entry still in preferences.md")
	}
	if !strings.Contains(string(src), "musgo") {
		t.Error("the recent entry was archived too")
	}
	if !strings.HasPrefix(string(src), "---\nid: preferences") {
		t.Error("header damaged")
	}
	arch, err := os.ReadFile(filepath.Join(k.dir, "archive", "preferences.md"))
	if err != nil {
		t.Fatalf("archive file missing: %v", err)
	}
	if !strings.Contains(string(arch), "pulpo") {
		t.Error("stale entry not in the archive file")
	}

	if hits, err := k.Recall("pulpo gallega"); err == nil && len(hits) != 0 {
		t.Errorf("aged fact still recalled: %v", hits)
	}
	if hits, _ := k.Recall("verde musgo"); len(hits) == 0 {
		t.Error("fresh fact no longer recalled")
	}

	if n2, err := k.ArchiveStale(30, time.Now()); err != nil || n2 != 0 {
		t.Errorf("second pass: n=%d err=%v, want n=0 (idempotent)", n2, err)
	}
}

// TestArchiveKeepsUnstampedAndRecent: hand-written lines (no stamp)
// and recent stamps never move - unknown age must not cost a fact.
func TestArchiveKeepsUnstampedAndRecent(t *testing.T) {
	k := openTemp(t)
	raw := "---\nid: preferences\n---\n# preferences\n" +
		"- Linea escrita a mano, sin sello\n" +
		"- Prefiere el verde musgo [" + time.Now().Format("2006-01-02") + ", origin: agent]\n"
	if err := os.WriteFile(filepath.Join(k.dir, "preferences.md"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := k.ArchiveStale(30, time.Now())
	if err != nil || n != 0 {
		t.Fatalf("archive: n=%d err=%v, want n=0", n, err)
	}
	if _, err := os.Stat(filepath.Join(k.dir, "archive", "preferences.md")); err == nil {
		t.Error("archive file created although nothing was stale")
	}
	src, _ := os.ReadFile(filepath.Join(k.dir, "preferences.md"))
	if !strings.Contains(string(src), "sin sello") || !strings.Contains(string(src), "musgo") {
		t.Errorf("entries lost:\n%s", src)
	}
}

// TestForgetReachesArchivedCopy: an aged-out entry must still obey
// olvida: - otherwise a forgotten fact would survive one read away in
// archive/ (the M3 lesson in a new location).
func TestForgetReachesArchivedCopy(t *testing.T) {
	k := openTemp(t)
	old := time.Now().AddDate(0, 0, -60).Format("2006-01-02")
	fresh := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	writeStamped(t, filepath.Join(k.dir, "preferences.md"), "preferences", old, fresh)
	if n, err := k.ArchiveStale(30, time.Now()); err != nil || n != 1 {
		t.Fatalf("archive: n=%d err=%v", n, err)
	}

	if n, err := k.ForgetAll("pulpo gallega"); err != nil || n != 1 {
		t.Fatalf("forget archived: n=%d err=%v, want n=1", n, err)
	}
	arch, err := os.ReadFile(filepath.Join(k.dir, "archive", "preferences.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(arch), "pulpo") {
		t.Errorf("the archived copy survived olvida::\n%s", arch)
	}
	if !strings.Contains(string(arch), "musgo") && !strings.Contains(
		mustRead(t, filepath.Join(k.dir, "preferences.md")), "musgo") {
		t.Error("forget removed the wrong fact")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
