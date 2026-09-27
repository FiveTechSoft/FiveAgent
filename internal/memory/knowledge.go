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
	"time"
	"unicode"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"gopkg.in/yaml.v3"
)

// Knowledge is the open memory folder.
type Knowledge struct {
	dir  string
	repo *git.Repository
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
	return k, nil
}

// Append adds one bullet to the file with the given id and commits the
// change. The id is the file name without extension ("people",
// "preferences", "workstreams"). It reports false when the note is
// already stored (same text, ignoring case, spacing and trailing
// punctuation) and adds nothing.
func (k *Knowledge) Append(id, entry string) (bool, error) {
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
	return true, k.commit("memory: note in "+id, name)
}

// Forget removes every bullet containing match (case-insensitive) from
// the file with the given id and commits the removal. Headings and the
// YAML header are never touched. It returns how many bullets were
// removed.
func (k *Knowledge) Forget(id, match string) (int, error) {
	name := id + ".md"
	path := filepath.Join(k.dir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("memory: unknown file %q (have: %s)", id, strings.Join(k.fileNames(), ", "))
	}
	match = strings.ToLower(strings.TrimSpace(match))
	var kept []string
	removed := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "- ") && strings.Contains(strings.ToLower(ln), match) {
			removed++
			continue
		}
		kept = append(kept, ln)
	}
	if removed == 0 {
		return 0, nil
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		return 0, err
	}
	return removed, k.commit("memory: forget in "+id, name)
}

// normalizeNote reduces a note to a comparable form: lowercase, single
// spaces, no trailing punctuation.
func normalizeNote(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, " .;,")
}

// Recall returns the memory lines relevant to a query, best files first.
// Scoring: an alias match counts 3, each matching body line counts 1.
// A file matched only by alias returns its whole body (files are small
// at this stage).
func (k *Knowledge) Recall(query string) ([]FileHit, error) {
	terms := queryTerms(query)
	if len(terms) == 0 {
		return nil, nil
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
		hits = append(hits, FileHit{ID: f.ID, Score: score, Lines: lines})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > 5 {
		hits = hits[:5]
	}
	return hits, nil
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
