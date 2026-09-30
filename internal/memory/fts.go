package memory

// Stage 7c: an embedded SQLite FTS5 index over the memory files, as a
// rebuildable cache. The markdown files stay the source of truth; the
// index is disposable - it rebuilds from the files on open whenever
// their content hash drifted, and Recall falls back to the keyword
// path on any index error, so a broken or missing index is never
// worse than no index. The driver is modernc.org/sqlite (pure Go):
// the agent binary and the release workflow stay cgo-free.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

type ftsIndex struct {
	db *sql.DB
	// queries counts the recalls served through FTS5; tests read it to
	// prove the index path really fires (a recall that silently fell
	// back to keywords would leave it at zero).
	queries int
}

// openFTS opens (and when needed rebuilds) the index for the knowledge
// dir. A failure is not fatal: the caller keeps the keyword path.
func (k *Knowledge) openFTS() {
	db, err := sql.Open("sqlite", filepath.Join(k.dir, "fts5.index.db"))
	if err != nil {
		return
	}
	ix := &ftsIndex{db: db}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		db.Close()
		return
	}
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS bullets USING fts5(file_id UNINDEXED, pos UNINDEXED, body)`); err != nil {
		// No FTS5 in this build: keyword recall stays the only path.
		db.Close()
		return
	}
	k.ix = ix
	if k.filesHash() != ix.storedHash() {
		_ = k.reindexLocked()
	}
}

// filesHash is the drift detector: one hash over every .md file's name
// and content. Any edit, append, forget or consolidation changes it.
func (k *Knowledge) filesHash() string {
	h := sha256.New()
	for _, name := range k.fileNames() {
		raw, err := os.ReadFile(filepath.Join(k.dir, name))
		if err != nil {
			continue
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (ix *ftsIndex) storedHash() string {
	var v string
	if err := ix.db.QueryRow(`SELECT value FROM meta WHERE key='hash'`).Scan(&v); err != nil {
		return ""
	}
	return v
}

// reindexLocked rebuilds the whole index from the files. The caller
// holds k.mu. Total rebuild keeps the logic drift-proof; memory files
// are small by design, so the cost is noise.
func (k *Knowledge) reindexLocked() error {
	if k.ix == nil {
		return nil
	}
	tx, err := k.ix.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM bullets`); err != nil {
		return err
	}
	for _, name := range k.fileNames() {
		f, err := k.readFile(name)
		if err != nil {
			continue
		}
		pos := 0
		for _, ln := range strings.Split(f.Body, "\n") {
			if t := strings.TrimSpace(ln); strings.HasPrefix(t, "- ") {
				if _, err := tx.Exec(`INSERT INTO bullets (file_id, pos, body) VALUES (?, ?, ?)`, f.ID, pos, t); err != nil {
					return err
				}
				pos++
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('hash', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k.filesHash()); err != nil {
		return err
	}
	return tx.Commit()
}

// ftsRecall returns the bullets matching the query terms through FTS5,
// grouped per file and ordered oldest to newest, or an error if the
// index cannot serve the query (the caller then falls back to the
// keyword path).
func (k *Knowledge) ftsRecall(terms []string) (map[string][]string, error) {
	if k.ix == nil || len(terms) == 0 {
		return nil, fmt.Errorf("fts: no index or no terms")
	}
	// One quoted phrase per term, OR-ed: the same any-term-matches
	// semantics as the keyword path.
	quoted := make([]string, 0, len(terms))
	for _, t := range terms {
		quoted = append(quoted, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
	}
	rows, err := k.ix.db.Query(`SELECT file_id, pos, body FROM bullets WHERE bullets MATCH ? ORDER BY file_id, pos`,
		strings.Join(quoted, " OR "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, body string
		var pos int
		if err := rows.Scan(&id, &pos, &body); err != nil {
			return nil, err
		}
		out[id] = append(out[id], body)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	k.ix.queries++
	return out, nil
}

// ftsHits shapes FTS results into FileHits with the same scoring and
// caps as the keyword path: alias match counts 3, each matching bullet
// counts 1, a file contributes at most maxLinesPerFile bullets (the
// most recent), and at most 5 files ride.
func (k *Knowledge) ftsHits(query string, terms []string) ([]FileHit, error) {
	byFile, err := k.ftsRecall(terms)
	if err != nil {
		return nil, err
	}
	var hits []FileHit
	for _, name := range k.fileNames() {
		f, err := k.readFile(name)
		if err != nil {
			continue
		}
		score := 0
		for _, a := range f.Aliases {
			for _, t := range terms {
				if strings.EqualFold(a, t) {
					score += 3
				}
			}
		}
		lines := byFile[f.ID]
		score += len(lines)
		if score == 0 {
			continue
		}
		if len(lines) == 0 {
			lines = nonEmptyLines(f.Body, 20)
		}
		if len(lines) > maxLinesPerFile {
			lines = lines[len(lines)-maxLinesPerFile:]
		}
		hits = append(hits, FileHit{ID: f.ID, Score: score, Lines: lines})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > 5 {
		hits = hits[:5]
	}
	return hits, nil
}
