package memory

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// stampDateRE extracts the date part of the provenance stamp Append
// writes at the end of a bullet ("[2026-10-02, origin: save_memory]").
var stampDateRE = regexp.MustCompile(`\[(\d{4}-\d{2}-\d{2}), origin: [^\]\n]*\]`)

// entryStampDate returns the date stamped on any line of an entry
// (Append stamps the bullet itself; continuation lines carry none).
// An entry without a parseable stamp has unknown age.
func entryStampDate(entry []string) (time.Time, bool) {
	for _, ln := range entry {
		if m := stampDateRE.FindStringSubmatch(ln); m != nil {
			if d, err := time.Parse("2006-01-02", m[1]); err == nil {
				return d, true
			}
		}
	}
	return time.Time{}, false
}

// ArchiveStale ages entries out of the working set (stage 7l): every
// stamped entry older than days moves from <scope>/<file>.md into
// <scope>/archive/<file>.md. The archive/ subdirectory is invisible to
// Recall, the FTS index and the frozen snapshot - all three walk only
// the top-level .md files (fileNames) - so an aged fact stops
// resurfacing while staying on disk and in git history, one read away.
//
// Safety rules: entries without a stamp (hand-written lines) are never
// archived - unknown age must not cost a fact; the archived copy keeps
// the entry text verbatim so ForgetAll still finds it (olvida: reaches
// archive/ too, via forgetFiles); days <= 0 disables the pass. It
// returns how many entries moved.
func (k *Knowledge) ArchiveStale(days int, now time.Time) (int, error) {
	if days <= 0 {
		return 0, nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	cutoff := now.AddDate(0, 0, -days)
	moved := 0
	var changed []string
	for _, name := range k.fileNames() {
		raw, err := os.ReadFile(filepath.Join(k.dir, name))
		if err != nil {
			continue
		}
		front, entries := splitEntries(strings.Split(string(raw), "\n"))
		kept := append([]string(nil), front...)
		var stale [][]string
		for _, e := range entries {
			if d, ok := entryStampDate(e); ok && d.Before(cutoff) {
				stale = append(stale, e)
				continue
			}
			kept = append(kept, e...)
		}
		if len(stale) == 0 {
			continue
		}
		archPath := filepath.Join(k.dir, "archive", name)
		archBody := ""
		if ab, err := os.ReadFile(archPath); err == nil {
			archBody = strings.TrimRight(string(ab), "\n") + "\n"
		} else {
			stem := strings.TrimSuffix(name, ".md")
			archBody = "---\nid: " + stem + "-archive\n---\n# " + stem + " archive\n"
		}
		for _, e := range stale {
			block := strings.Join(e, "\n")
			if !strings.Contains(strings.ToLower(archBody), strings.ToLower(strings.TrimSpace(block))) {
				archBody += block + "\n"
			}
			moved++
		}
		if err := os.WriteFile(filepath.Join(k.dir, name), []byte(strings.Join(kept, "\n")), 0o600); err != nil {
			return moved, err
		}
		if err := os.MkdirAll(filepath.Join(k.dir, "archive"), 0o700); err != nil {
			return moved, err
		}
		if err := os.WriteFile(archPath, []byte(archBody), 0o600); err != nil {
			return moved, err
		}
		changed = append(changed, name, filepath.Join("archive", name))
	}
	if moved == 0 {
		return 0, nil
	}
	if err := k.commit("memory: archive stale entries", changed...); err != nil {
		return moved, err
	}
	return moved, k.reindexLocked()
}
