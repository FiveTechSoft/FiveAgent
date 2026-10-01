// Package trajectory records what the agent DID, not just pass/fail
// (stage 12 of docs/ROADMAP.md): every turn as a JSONL trajectory -
// the messages (user/assistant/tool, with the tool calls and their
// results), the outcome, and per-tool usage stats - in the message
// shape a fine-tuning dataset consumes, so stage 13 can train on the
// surviving trajectories directly.
//
// Two sinks over one record format: the rotating session log
// (opt-in via trajectory.enabled in fiveagent.yml, for real sessions)
// and per-case files for battery runs (Logger.LogCase), with a
// run-comparison report (Compare) so two battery runs diff by their
// artifacts.
//
// Nothing sensitive is ever written: every recorded string goes
// through Redact (emails, phone-shaped numbers, bearer tokens and
// common secret shapes become <redacted>, the same spirit as the
// browser audit's password redaction), and the record carries the
// channel, never the user's identity.
package trajectory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/model"
)

// Message is one turn message in dataset shape.
type Message struct {
	Role      string     `json:"role"` // user | assistant | tool
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Name      string     `json:"name,omitempty"` // tool name on role=tool
}

// ToolCall is one invocation the model asked for.
type ToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Stat is the per-tool usage tally for one trajectory.
type Stat struct {
	Calls int `json:"calls"`
	OK    int `json:"ok"`
	Fail  int `json:"fail"`
}

// Outcome closes a trajectory: what came back and how the turn went.
type Outcome struct {
	Reply      string `json:"reply"`
	DurationMs int64  `json:"duration_ms"`
	ToolRounds int    `json:"tool_rounds"`
	Error      string `json:"error,omitempty"`
}

// ModelAttempt is one native HTTP attempt, separate from tool-round accounting.
type ModelAttempt struct {
	Sequence int `json:"sequence"`
	model.NativeObservation
	ThinkingChars     int  `json:"thinking_bytes"`
	ThinkingTruncated bool `json:"thinking_truncated,omitempty"`
	ContentTruncated  bool `json:"content_truncated,omitempty"`
}

// Record is one trajectory. User identity is deliberately absent:
// the dataset needs behavior, not people.
type Record struct {
	ModelAttempts []ModelAttempt   `json:"model_attempts,omitempty"`
	ID            string           `json:"id"`
	StartedAt     time.Time        `json:"started_at"`
	EndedAt       time.Time        `json:"ended_at"`
	Channel       string           `json:"channel"`
	Messages      []Message        `json:"messages"`
	Outcome       Outcome          `json:"outcome"`
	ToolStats     map[string]*Stat `json:"tool_stats"`
}

// AddToolCall tallies one executed tool call.
func (r *Record) AddToolCall(name string, ok bool) {
	if r.ToolStats == nil {
		r.ToolStats = map[string]*Stat{}
	}
	st := r.ToolStats[name]
	if st == nil {
		st = &Stat{}
		r.ToolStats[name] = st
	}
	st.Calls++
	if ok {
		st.OK++
	} else {
		st.Fail++
	}
}

var (
	reEmail  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	rePhone  = regexp.MustCompile(`\+?\d[\d .-]{7,}\d`)
	reBearer = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	reSecret = regexp.MustCompile(`(?i)("?(?:token|api[_-]?key|secret|password|passwd)"?\s*[:=]\s*"?)[^\s",}]+`)
)

// Redact strips the shapes that must never land in a dataset: email
// addresses, phone-shaped numbers, bearer tokens and token/secret
// key-values. It is deliberately conservative: a redacted byte too
// many beats a leaked one.
func Redact(s string) string {
	s = reBearer.ReplaceAllString(s, "${1}<redacted>")
	s = reSecret.ReplaceAllString(s, "${1}<redacted>")
	s = reEmail.ReplaceAllString(s, "<redacted>")
	s = rePhone.ReplaceAllString(s, "<redacted>")
	return s
}

// RedactRecord applies Redact to every free-text field of a record.
func RedactRecord(r *Record) {
	for i := range r.ModelAttempts {
		o := &r.ModelAttempts[i]
		o.Model = Redact(o.Model)
		o.Purpose = Redact(o.Purpose)
		o.Thinking = Redact(o.Thinking)
		o.DoneReason = Redact(o.DoneReason)
		o.Content = Redact(o.Content)
		o.Error = Redact(o.Error)
	}
	for i := range r.Messages {
		r.Messages[i].Content = Redact(r.Messages[i].Content)
		for j := range r.Messages[i].ToolCalls {
			r.Messages[i].ToolCalls[j].Arguments = Redact(r.Messages[i].ToolCalls[j].Arguments)
		}
	}
	r.Outcome.Reply = Redact(r.Outcome.Reply)
	r.Outcome.Error = Redact(r.Outcome.Error)
}

// Logger writes trajectories. The rotating session log keeps at most
// MaxFiles files of at most MaxBytes each; per-case logs (battery
// runs) are one file per case under the same dir.
type Logger struct {
	Dir      string
	MaxBytes int64 // per rotating file; default 10 MB
	MaxFiles int   // rotating files kept; default 5
}

// Open creates the logger's directory.
func Open(dir string, maxMB, maxFiles int) (*Logger, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("trajectory: a directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Logger{Dir: dir, MaxBytes: int64(maxMB) << 20, MaxFiles: maxFiles}
	if l.MaxBytes <= 0 {
		l.MaxBytes = 10 << 20
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = 5
	}
	return l, nil
}

const sessionFile = "trajectories.jsonl"

// Log appends one record to the rotating session log.
func (l *Logger) Log(r Record) error {
	if err := l.rotateIfNeeded(); err != nil {
		return err
	}
	return l.append(filepath.Join(l.Dir, sessionFile), r)
}

// LogCase writes one record to a per-case file (battery runs: one
// JSONL trajectory per case).
func (l *Logger) LogCase(caseID string, r Record) error {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		}
		return '_'
	}, caseID)
	if clean == "" {
		return fmt.Errorf("trajectory: empty case id")
	}
	return l.append(filepath.Join(l.Dir, clean+".jsonl"), r)
}

func (l *Logger) append(path string, r Record) error {
	RedactRecord(&r)
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// rotateIfNeeded rolls trajectories.jsonl to a timestamped sibling
// when it passes MaxBytes, keeping at most MaxFiles rotated files.
func (l *Logger) rotateIfNeeded() error {
	path := filepath.Join(l.Dir, sessionFile)
	st, err := os.Stat(path)
	if err != nil || st.Size() < l.MaxBytes {
		return nil // no file yet, or still under the cap: append away
	}
	rotated := filepath.Join(l.Dir, fmt.Sprintf("trajectories-%s.jsonl", time.Now().UTC().Format("20060102-150405")))
	if err := os.Rename(path, rotated); err != nil {
		return err
	}
	matches, _ := filepath.Glob(filepath.Join(l.Dir, "trajectories-*.jsonl"))
	sort.Strings(matches) // timestamped names sort oldest first
	for len(matches) >= l.MaxFiles {
		if err := os.Remove(matches[0]); err != nil {
			return err
		}
		matches = matches[1:]
	}
	return nil
}

// Compare diffs two runs by their artifacts: per case file, the
// trajectories' tool-call counts and failures. It answers "what
// changed between run A and run B" from the logs, not from memory.
func Compare(oldDir, newDir string) (string, error) {
	oldStats, err := readDirStats(oldDir)
	if err != nil {
		return "", err
	}
	newStats, err := readDirStats(newDir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	cases := map[string]bool{}
	for c := range oldStats {
		cases[c] = true
	}
	for c := range newStats {
		cases[c] = true
	}
	names := make([]string, 0, len(cases))
	for c := range cases {
		names = append(names, c)
	}
	sort.Strings(names)
	changes := 0
	for _, c := range names {
		o, n := oldStats[c], newStats[c]
		if o != n {
			changes++
			fmt.Fprintf(&b, "%s: %s -> %s\n", c, o, n)
		}
	}
	if changes == 0 {
		return "no trajectory differences between the two runs", nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// readDirStats summarizes each case file as "turns/tool-calls/fails".
func readDirStats(dir string) (map[string]string, error) {
	out := map[string]string{}
	matches, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil || len(matches) == 0 {
		return out, err
	}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			return nil, err
		}
		turns, calls, fails := 0, 0, 0
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line == "" {
				continue
			}
			var r Record
			if err := json.Unmarshal([]byte(line), &r); err != nil {
				return nil, fmt.Errorf("%s: corrupt trajectory line: %w", m, err)
			}
			turns++
			for _, st := range r.ToolStats {
				calls += st.Calls
				fails += st.Fail
			}
		}
		out[strings.TrimSuffix(filepath.Base(m), ".jsonl")] = fmt.Sprintf("%d turns, %d tool calls, %d failures", turns, calls, fails)
	}
	return out, nil
}

// AddModelAttempt bounds retained text and strips private values before storage.
// Counts describe original response bytes, not tokenizer counts.
func (r *Record) AddModelAttempt(o model.NativeObservation) {
	a := ModelAttempt{Sequence: len(r.ModelAttempts) + 1, NativeObservation: o, ThinkingChars: len(o.Thinking)}
	a.Thinking, a.ThinkingTruncated = boundedTelemetry(Redact(o.Thinking), 4096)
	a.Content, a.ContentTruncated = boundedTelemetry(Redact(o.Content), 4096)
	a.Error, _ = boundedTelemetry(Redact(o.Error), 1024)
	r.ModelAttempts = append(r.ModelAttempts, a)
}
func boundedTelemetry(s string, n int) (string, bool) {
	if len(s) <= n {
		return s, false
	}
	for n > 0 && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n], true
}
