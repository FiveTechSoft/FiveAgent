// Knowledge is FiveAgent's long-term memory: a folder of plain markdown
// files (people.md, preferences.md, workstreams.md to start) versioned
// with git. The markdown files are the source of truth; git history is
// the audit trail. Retrieval is by keyword and per-file aliases.
//
// Each file starts with a small YAML header:
//
//	---
//	id: people
//	aliases: [personas, gente, contacts, contactos]
//	---
//	# People
//
//	- María is his sister; noted 2026-09-28.
//
// Aliases let a recall for "contactos" find the people file even when
// the word never appears in its body.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"
)

// Knowledge is the open memory folder.
type Knowledge struct {
	// mu serializes reads and writes: Append/Forget are read-modify-write
	// cycles and concurrent senders otherwise lose facts silently
	// (measured: 5-7 of 8 concurrent saves landed, evals/
	// TestConcurrentSaves). Memory operations are fast, so one mutex for
	// everything is the simple correct answer.
	mu   sync.Mutex
	dir  string
	repo *git.Repository
	ix *ftsIndex // stage 7c: rebuildable FTS5 cache; nil = keyword path only
}

// File is one parsed memory file.
type File struct {
	ID      string
	Aliases []string
	Body    string
}

// FileHit is one file matched by Recall, with the lines that matched.
type FileHit struct {
	ID    string
	Score int
	Lines []string // matching lines, or the whole body when only aliases matched
}

// header is the YAML front-matter of a memory file.
type header struct {
	ID      string   `yaml:"id"`
	Aliases []string `yaml:"aliases"`
}

// defaultFiles are the three files the minimal memory starts with. The
// structure grows from real use, not up front.
var defaultFiles = map[string]string{
	"people.md": `---
id: people
aliases: [personas, gente, contacts, contactos, family, familia, friends, amigos]
---
# People

Who the user knows and how they relate.
`,
	"preferences.md": `---
id: preferences
aliases: [preferencias, gustos, prefs, likes, dislikes]
---
# Preferences

What the user likes, dislikes and asks for repeatedly.
`,
	"workstreams.md": `---
id: workstreams
aliases: [trabajo, proyectos, projects, tasks, tareas, work, ongoing]
---
# Workstreams

Ongoing work and its current state.
`,
	"learnings.md": `---
id: learnings
aliases: [aprendizajes, lessons, errores, mistakes, corrections, correcciones, feedback]
---
# Learnings

Short self-critiques the agent writes when a task fails or the user
corrects it (roadmap stage 7f), plus explicit reaction feedback. Read
as data in later turns so the same mistake is not repeated.
`,
	"digests.md": `---
id: digests
aliases: [resumen, resúmenes, summaries, digest, sesiones, sessions, conversaciones]
---
# Session digests

Rolling summaries of the conversation turns that context pruning
compacted away (roadmap stage 7g): detail dropped from the live
history survives here as data, recallable in later turns.
`,
}

// OpenKnowledge opens (or creates) the memory folder at dir. Missing
// default files are written and committed; the folder is a git repo,
// initialized on first use.
func OpenKnowledge(dir string) (*Knowledge, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	repo, err := git.PlainOpen(dir)
	if err == git.ErrRepositoryNotExists {
		repo, err = git.PlainInit(dir, false)
	}
	if err != nil {
		return nil, fmt.Errorf("memory: open git repo: %w", err)
	}
	k := &Knowledge{dir: dir, repo: repo}
	var created []string
	for name, tpl := range defaultFiles {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(tpl), 0o600); err != nil {
				return nil, err
			}
			created = append(created, name)
		}
	}
	if len(created) > 0 {
		if err := k.commit("memory: start", created...); err != nil {
			return nil, err
		}
	}
	// Stage 7c: open the FTS5 cache (rebuilding it when the files
	// drifted). A failure leaves ix nil and the keyword path serving.
	k.openFTS()
	return k, nil
}

// Dir returns the knowledge folder path (the global scope root).
func (k *Knowledge) Dir() string { return k.dir }

// Append adds one bullet to the file with the given id and commits the
// change. The id is the file name without extension ("people",
// "preferences", "workstreams"). It reports false when the note is
// already stored (same text, ignoring case, spacing and trailing
// punctuation) and adds nothing.
func (k *Knowledge) Append(id, entry string) (bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	name := id + ".md"
	path := filepath.Join(k.dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("memory: unknown file %q (have: %s)", id, strings.Join(k.fileNames(), ", "))
	}
	entry = strings.TrimSpace(entry)
	if !strings.HasPrefix(entry, "- ") {
		entry = "- " + entry
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		old := normalizeNote(strings.TrimPrefix(strings.TrimSpace(ln), "- "))
		cur := normalizeNote(strings.TrimPrefix(entry, "- "))
		if old != "" && (old == cur || strings.Contains(old, cur)) {
			return false, nil
		}
	}
	body := strings.TrimRight(string(raw), "\n") + "\n" + entry + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return false, err
	}
	if err := k.commit("memory: note in "+id, name); err != nil {
		return true, err
	}
	return true, k.reindexLocked()
}

// Forget removes every bullet containing match (case-insensitive) from
// the file with the given id and commits the removal. Headings and the
// YAML header are never touched. It returns how many bullets were
// removed.
func (k *Knowledge) Forget(id, match string) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	name := id + ".md"
	if _, err := os.Stat(filepath.Join(k.dir, name)); err != nil {
		return 0, fmt.Errorf("memory: unknown file %q (have: %s)", id, strings.Join(k.fileNames(), ", "))
	}
	removed, err := k.forgetFile(name, match)
	if err != nil || removed == 0 {
		return removed, err
	}
	if err := k.commit("memory: forget in "+id, name); err != nil {
		return removed, err
	}
	return removed, k.reindexLocked()
}

// ForgetAll removes every bullet containing match from every memory file
// of this scope - the global files (people, preferences, workstreams,
// learnings, digests) or one sender's scope under users/<id>. Each
// changed file gets its own commit.
//
// It exists because a fact survives in more than one file: context
// pruning (stage 7g) copies the compacted turn into the sender's
// digests.md, and Recall merges that scope back in. Forgetting the
// token from the three curated files only left the digest copy alive,
// so the fact resurrected after a restart (battery run 6: M3 0/1 plus
// the "invented" token that was really still on disk). Scopes are not
// crossed: this walks the files of k.dir, never users/ subfolders, so
// one sender's "olvida:" never reaches another sender's memory.
func (k *Knowledge) ForgetAll(match string) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	match = strings.ToLower(strings.TrimSpace(match))
	if match == "" {
		return 0, nil
	}
	removed := 0
	var changed []string
	for _, name := range k.fileNames() {
		n, err := k.forgetFile(name, match)
		if err != nil {
			continue
		}
		if n > 0 {
			removed += n
			changed = append(changed, name)
		}
	}
	if removed == 0 {
		return 0, nil
	}
	if err := k.commit("memory: forget", changed...); err != nil {
		return removed, err
	}
	return removed, k.reindexLocked()
}

// forgetFile strips the matching entries from one file, without
// committing. It writes nothing when no entry matches.
//
// An ENTRY is a "- " bullet plus the unbulleted lines under it: a stage
// 7g session digest is a header bullet and one or more paragraphs, so
// dropping only the header would leave the fact on disk (battery M3,
// still 0/1 in run 7 with the purge running at the right time).
//
// A line matches when it carries the query verbatim (the classic rule),
// or when it carries at least two of the query's content words: the
// digest's own summarizer paraphrases, and "mi plato de fiesta" never
// appears verbatim in "El plato de fiesta del usuario es la empanada".
// Two words is the floor because one common word is too wide a net for
// a destructive command.
func (k *Knowledge) forgetFile(name, match string) (int, error) {
	path := filepath.Join(k.dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	match = strings.ToLower(strings.TrimSpace(match))
	if match == "" {
		return 0, nil
	}
	tokens := forgetTokens(match)
	var kept, entry []string
	removed := 0
	inEntry, hit := false, false
	flush := func() {
		if !inEntry {
			return
		}
		if hit {
			removed++
		} else {
			kept = append(kept, entry...)
		}
		entry, inEntry, hit = nil, false, false
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "- ") {
			flush()
			inEntry = true
		}
		if !inEntry { // front matter and intro prose: never removable
			kept = append(kept, ln)
			continue
		}
		if forgetLineMatches(strings.ToLower(ln), match, tokens) {
			hit = true
		}
		entry = append(entry, ln)
	}
	flush()
	if removed == 0 {
		return 0, nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		return 0, err
	}
	return removed, nil
}

// forgetStopwords are the query words that carry no topic of their own.
// Two-letter words are dropped by length in forgetTokens; these are the
// longer ones ("todo lo que sepas sobre mi comida favorita" would other-
// wise vote for "sepas" as if it named a fact).
var forgetStopwords = map[string]bool{
	"que": true, "por": true, "para": true, "con": true, "sin": true,
	"del": true, "los": true, "las": true, "una": true, "unos": true,
	"unas": true, "este": true, "esta": true, "estos": true, "estas": true,
	"ese": true, "esa": true, "esos": true, "esas": true, "aquel": true,
	"aquella": true, "más": true, "muy": true, "también": true,
	"todo": true, "toda": true, "todos": true, "todas": true,
	"otro": true, "otra": true, "otros": true, "otras": true,
	"nada": true, "algo": true, "cual": true, "cuales": true,
	"donde": true, "cuando": true, "como": true, "sobre": true,
	"entre": true, "hasta": true, "desde": true, "cuyo": true,
	"the": true, "and": true, "for": true, "you": true, "are": true,
	"was": true, "not": true, "all": true, "any": true, "can": true,
	"has": true, "have": true, "this": true, "that": true, "with": true,
	"from": true, "into": true, "what": true, "when": true, "how": true,
}

// forgetTokens returns the query's content words: alphabetic, three
// letters or more, not stopwords, no repeats.
func forgetTokens(match string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(match, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		w = strings.ToLower(w)
		if len(w) < 3 || forgetStopwords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// forgetLineMatches decides whether one line of an entry belongs to the
// query: verbatim substring, or at least two content words (one when the
// query has only one).
func forgetLineMatches(line, match string, tokens []string) bool {
	if strings.Contains(line, match) {
		return true
	}
	if len(tokens) == 0 {
		return false
	}
	need := 2
	if len(tokens) == 1 {
		need = 1
	}
	got := 0
	for _, t := range tokens {
		if strings.Contains(line, t) {
			got++
			if got >= need {
				return true
			}
		}
	}
	return false
}

// Consolidate merges near-duplicate bullets in every file of the scope
// (stage 7l, rules slice): when one normalized bullet contains another,
// the longer one already says everything the shorter one says, so the
// shorter one goes and no fact is lost. Headings and headers are never
// touched. One commit per changed file. Returns the bullets merged away.
// Aging out, re-filing and the model summary pass stay pending.
func (k *Knowledge) Consolidate() (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	merged := 0
	for _, name := range k.fileNames() {
		raw, err := os.ReadFile(filepath.Join(k.dir, name))
		if err != nil {
			continue
		}
		lines := strings.Split(string(raw), "\n")
		type bullet struct {
			idx  int
			norm string
		}
		var bs []bullet
		for i, ln := range lines {
			if t := strings.TrimSpace(ln); strings.HasPrefix(t, "- ") {
				bs = append(bs, bullet{i, normalizeNote(strings.TrimPrefix(t, "- "))})
			}
		}
		drop := map[int]bool{}
		for i := 0; i < len(bs); i++ {
			for j := 0; j < len(bs); j++ {
				if i == j || bs[i].norm == "" || bs[j].norm == "" || drop[bs[j].idx] {
					continue
				}
				switch {
				case bs[i].norm == bs[j].norm && i < j:
					drop[bs[j].idx] = true // exact duplicate: the first stays
				case len(bs[i].norm) > len(bs[j].norm) && strings.Contains(bs[i].norm, bs[j].norm):
					drop[bs[j].idx] = true // the longer one already says it all
				}
			}
		}
		if len(drop) == 0 {
			continue
		}
		var kept []string
		for i, ln := range lines {
			if !drop[i] {
				kept = append(kept, ln)
			}
		}
		if err := os.WriteFile(filepath.Join(k.dir, name), []byte(strings.Join(kept, "\n")), 0o600); err != nil {
			return merged, err
		}
		if err := k.commit("memory: consolidate "+name, name); err != nil {
			return merged, err
		}
		merged += len(drop)
	}
	if merged > 0 {
		if err := k.reindexLocked(); err != nil {
			return merged, err
		}
	}
	return merged, nil
}

// normalizeNote reduces a note to a comparable form: lowercase, single
// spaces, no trailing punctuation.
func normalizeNote(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, " .;,")
}

// maxLinesPerFile caps how many matching bullets one file contributes
// to a recall. Without it a popular word injects the whole file and the
// per-turn cost grows linearly with the store.
const maxLinesPerFile = 10

// Recall returns the memory lines relevant to a query, best files first.
// Scoring: an alias match counts 3, each matching body line counts 1.
// A file matched only by alias returns its whole body (files are small
// at this stage). One file contributes at most maxLinesPerFile matching
// bullets, keeping the most recent.
func (k *Knowledge) Recall(query string) ([]FileHit, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	terms := queryTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}
	// Stage 7c: the FTS5 cache serves first; on any index error the
	// keyword path below answers, never worse than no index.
	if hits, err := k.ftsHits(query, terms); err == nil {
		return hits, nil
	}
	var hits []FileHit
	for _, name := range k.fileNames() {
		f, err := k.readFile(name)
		if err != nil {
			return nil, err
		}
		score := 0
		for _, a := range f.Aliases {
			for _, t := range terms {
				if strings.EqualFold(a, t) {
					score += 3
				}
			}
		}
		var lines []string
		for _, ln := range strings.Split(f.Body, "\n") {
			// Facts live in bullets; headings and description lines are
			// scaffolding and must not score (their common words - "Who
			// the user knows..." - otherwise pollute recall).
			if !strings.HasPrefix(strings.TrimSpace(ln), "- ") {
				continue
			}
			low := strings.ToLower(ln)
			for _, t := range terms {
				if strings.Contains(low, t) {
					lines = append(lines, ln)
					score++
					break
				}
			}
		}
		if score == 0 {
			continue
		}
		if len(lines) == 0 {
			lines = nonEmptyLines(f.Body, 20)
		}
		if len(lines) > maxLinesPerFile {
			// Notes are appended chronologically; past the cap keep the
			// most recent matches so injection cost stays bounded as the
			// store grows (measured: ~739 tokens/turn at 50 notes without
			// a cap, evals/TestInjectionCostScaling).
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

// Snapshot renders the whole scope as one frozen block for the stage
// 7k session snapshot: every file's most recent bullets (maxPerFile
// per file - the same bounded-cost rule as recall), capped at maxChars
// total. Empty files are skipped; empty memory yields "". Deterministic:
// same files on disk, same block, byte for byte (the M5 gate relies on
// it). Only bullets ride: headers and alias lines are scaffolding.
func (k *Knowledge) Snapshot(maxPerFile, maxChars int) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	var b strings.Builder
	for _, name := range k.fileNames() {
		f, err := k.readFile(name)
		if err != nil {
			continue
		}
		var lines []string
		for _, ln := range strings.Split(f.Body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(ln), "- ") {
				lines = append(lines, strings.TrimSpace(ln))
			}
		}
		if len(lines) == 0 {
			continue
		}
		if len(lines) > maxPerFile {
			lines = lines[len(lines)-maxPerFile:]
		}
		for _, ln := range lines {
			if b.Len()+len(ln)+len(f.ID)+8 > maxChars {
				return b.String()
			}
			b.WriteString("[" + f.ID + "] " + ln + "\n")
		}
	}
	return b.String()
}

// commit stages the named files and commits them as the agent.
func (k *Knowledge) commit(msg string, files ...string) error {
	w, err := k.repo.Worktree()
	if err != nil {
		return err
	}
	for _, f := range files {
		if _, err := w.Add(f); err != nil {
			return err
		}
	}
	_, err = w.Commit(msg, &git.CommitOptions{Author: &object.Signature{
		Name: "fiveagent", Email: "fiveagent@localhost", When: time.Now(),
	}})
	return err
}

// fileNames lists the memory files, sorted for deterministic behavior.
func (k *Knowledge) fileNames() []string {
	ents, err := os.ReadDir(k.dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// readFile parses one memory file into header and body.
func (k *Knowledge) readFile(name string) (*File, error) {
	raw, err := os.ReadFile(filepath.Join(k.dir, name))
	if err != nil {
		return nil, err
	}
	f := &File{ID: strings.TrimSuffix(name, ".md"), Body: string(raw)}
	s := string(raw)
	if strings.HasPrefix(s, "---\n") {
		if end := strings.Index(s[4:], "\n---\n"); end >= 0 {
			var h header
			if err := yaml.Unmarshal([]byte(s[4:4+end]), &h); err == nil {
				if h.ID != "" {
					f.ID = h.ID
				}
				f.Aliases = h.Aliases
				f.Body = s[4+end+5:]
			}
		}
	}
	return f, nil
}

// queryTerms splits a query into lowercase search terms of 3+ letters;
// shorter words ("a", "de", "is") would match almost everything.
func queryTerms(q string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(w)) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

// nonEmptyLines returns the first n non-empty lines of s.
func nonEmptyLines(s string, n int) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		out = append(out, ln)
		if len(out) >= n {
			break
		}
	}
	return out
}

// SafeUserDir maps a sender id (e.g. "34612345678" or a chat id) to a
// folder name without path separators, for per-user memory scopes.
func SafeUserDir(userID string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", "..", "_", ":", "_")
	return r.Replace(userID)
}

// OpenUserScope opens the per-sender knowledge folder under
// <root>/users/<safe-userID>: one sender's facts never surface for
// another (stages 7n and 7g). The global scope root itself is opened
// with OpenKnowledge.
func OpenUserScope(root, userID string) (*Knowledge, error) {
	return OpenKnowledge(filepath.Join(root, "users", SafeUserDir(userID)))
}
