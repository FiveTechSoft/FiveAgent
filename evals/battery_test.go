package evals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	stdlibstrconv "strconv"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/batteryreport"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/links"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
	"github.com/FiveTechSoft/FiveAgent/internal/trajectory"
	"gopkg.in/yaml.v3"
)

// batteryPrompt is one battery entry: prompt plus its scoring rubric.
type batteryPrompt struct {
	Prompt          string   `yaml:"prompt"`
	MustContain     []string `yaml:"must_contain"`
	MustNotContain  []string `yaml:"must_not_contain"`
	AbstainOK       bool     `yaml:"abstain_ok"`
	AbstainExpected bool     `yaml:"abstain_expected"`
	Setup           bool     `yaml:"setup"`          // conversation setup turn, no scoring
	NeedsSandbox    bool     `yaml:"needs_sandbox"`  // needs a run_command backend; skipped when unavailable
	AuditContains   string   `yaml:"audit_contains"` // after the turn, the audit log must hold a run_command line with this token
	MemoryWrites    string   `yaml:"memory_writes"`  // after the turn, the memory files on disk must hold this token (metric M1, write-through)
	MemoryErased    string   `yaml:"memory_erased"`  // after the turn, the memory files on disk must NOT hold this token (metric M3, effective forgetting on disk)
	RestartBefore   bool     `yaml:"restart_before"` // rebuild the agent (fresh history, same memory) before this prompt (metric M2, restart depth)
	PadTurns        int      `yaml:"pad_turns"`      // unscored filler turns before this prompt, pushing earlier setups past the history window (metric M2, deferred depth)
	Source          string   `yaml:"source"`
}

type batteryFile struct {
	Categories map[string][]batteryPrompt `yaml:"categories"`
}

func loadBattery(t *testing.T) batteryFile {
	t.Helper()
	raw, err := os.ReadFile("battery.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var bf batteryFile
	if err := yaml.Unmarshal(raw, &bf); err != nil {
		t.Fatal(err)
	}
	return bf
}

// TestBatteryFileValidates runs in CI: the battery must stay
// well-formed - every category non-empty, every prompt with at least
// one expectation, so a sloppy edit cannot silently weaken the battery.
func TestBatteryFileValidates(t *testing.T) {
	bf := loadBattery(t)
	total := 0
	for cat, ps := range bf.Categories {
		if len(ps) == 0 {
			t.Errorf("category %q is empty", cat)
		}
		for i, p := range ps {
			if strings.TrimSpace(p.Prompt) == "" {
				t.Errorf("%s[%d]: empty prompt", cat, i)
			}
			if len(p.MustContain)+len(p.MustNotContain) == 0 && !p.AbstainOK && !p.AbstainExpected && !p.Setup && p.AuditContains == "" && p.MemoryWrites == "" && p.MemoryErased == "" {
				t.Errorf("%s[%d]: no expectation (must_contain, must_not_contain, abstain_*, setup, audit_contains, memory_writes)", cat, i)
			}
		}
		total += len(ps)
	}
	t.Logf("METRIC battery file: %d prompts in %d categories, all with expectations", total, len(bf.Categories))
}

var spanishWeekdays = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// abstains reports whether the reply is an honest "I don't know".
// Match abstention INTENT phrases, not loose substrings: a bare
// "verificar" also matches correct advice like "hay que verificar si
// bash está instalado", which is not an abstention (real false positive
// in the 2026-09-28 live run).
// findingNegatives are natural "I searched and found nothing" openers.
var findingNegatives = []string{
	"no encontré", "no he encontrado", "no encuentro",
	"no pude encontrar", "no he podido encontrar",
	"no logré encontrar", "no he logrado encontrar",
}

// infoObjects are the things whose absence makes a finding-negative an
// abstention. "errores"/"diferencias" are deliberately absent: "no he
// encontrado errores" is a correct answer, not an abstention.
var infoObjects = []string{
	"información", "registro", "registros", "datos", "resultados",
	"fuentes", "referencias", "evidencia", "documentación", "constancia",
}

// dataRequests are follow-up asks for more data or a source, directed
// at the user. A finding-negative followed by one is an abstention even
// without an information word - 2026-09-29 live run (CPU-subset12,
// tiddlywinks Badajoz): "no encontré registros en línea... ¿puedes
// darme la fuente?" scored NO-ABSTENTION before this rule.
var dataRequests = []string{
	"¿puedes darme", "¿podrías darme", "¿me puedes dar", "¿me podrías dar",
	"¿tienes la fuente", "¿tienes alguna fuente", "¿tienes más datos",
	"pásame", "pasame", "dime la fuente", "indícame", "compárteme",
	"comparte la fuente", "si me das", "proporcióname", "facilita",
}

func containsAny(s string, ms []string) bool {
	for _, m := range ms {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// containsWithin reports whether any needle appears within maxDist
// bytes after any anchor occurrence.
func containsWithin(s string, anchors, needles []string, maxDist int) bool {
	for _, a := range anchors {
		i := strings.Index(s, a)
		if i < 0 {
			continue
		}
		tail := s[i+len(a):]
		if len(tail) > maxDist {
			tail = tail[:maxDist]
		}
		for _, n := range needles {
			if strings.Contains(tail, n) {
				return true
			}
		}
	}
	return false
}

func abstains(reply string) bool {
	low := strings.ToLower(reply)
	// Natural finding-negatives: abstention when the miss is about
	// information-like objects, or when the reply asks the user for
	// more data or a source right after.
	if containsWithin(low, findingNegatives, infoObjects, 60) {
		return true
	}
	if containsAny(low, findingNegatives) && containsAny(low, dataRequests) {
		return true
	}
	for _, m := range []string{
		"no lo sé", "no sé", "no estoy seguro", "no tengo información",
		"no puedo saber", "no lo conozco", "no tengo constancia",
		"no puedo verificar", "no puedo comprobar",
		"no tengo forma de verificar", "no tengo forma de comprobar",
		// Memory-domain abstentions (real false negatives in the
		// 2026-09-28 live run: "ahora no tengo registrada en mi memoria
		// cuál es tu comida favorita" was the correct answer after a
		// forget_memory setup, but scored NO-ABSTENTION).
		"no tengo registrado", "no tengo registrada",
		"no tengo en memoria", "no he guardado",
		// 2026-09-29 run 4: "No tengo guardado ese dato. ❌ Eliminada"
		// was the correct post-forget abstention and scored NO-ABSTENTION.
		"no tengo guardado", "no tengo guardada",
		// 2026-09-28 live run 3: "Ya he olvidado esa información...
		// no tengo registro" was the correct post-forget abstention
		// and scored NO-ABSTENTION.
		"he olvidado", "no tengo registro",
		// "No pude encontrar información..." openers: the tiddlywinks
		// prompt answered exactly that and scored NO-ABSTENTION. The
		// "información" word keeps "no he encontrado errores" (a correct
		// answer) out of the detector.
		"no pude encontrar información", "no he encontrado información",
		"no he podido encontrar información", "no encuentro información",
		// 2026-09-28 live run, two more detector gaps: "No encontré
		// información específica sobre..." (tiddlywinks) and
		// "¡No tengo esa información!" (memory after olvida:). Both
		// keep the "información" guard so "no he encontrado errores"
		// and "no tengo ese dato guardado, pero sí otros" style
		// answers stay out.
		"no encontré información", "no tengo esa información",
		// 2026-09-29 run 5: "no lo tengo en mi memoria" was the correct
		// post-forget abstention and scored NO-ABSTENTION.
		"no lo tengo en mi memoria", "no lo tengo en memoria",
		// 2026-10-02 run 7: two correct post-forget abstentions scored
		// NO-ABSTENTION - "Ya olvidé esa información" and "Ya no
		// recuerdo ..." after the olvida: setup.
		"ya olvid", "ya no recuerdo", "no recuerdo",
		// 2026-10-02 run 7: the tiddlywinks abstention opened with "No
		// consigo encontrar información documentada sobre ..." and
		// scored NO-ABSTENTION - the "información" guard keeps "no
		// consigo con ese comando" style answers out.
		"no consigo encontrar información",
		// 2026-10-02 run 7 literal: the two correct post-forget
		// abstentions that scored NO-ABSTENTION.
		"Ya olvidé esa información ✅",
		"Ya no recuerdo cuál es tu comida favorita.",
		"No consigo encontrar información documentada sobre una \"liga regional de tiddlywinks de Badajoz en 2023\".",
	} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// memoryFilesContain walks the knowledge dir and reports whether any
// markdown file holds the token, case-insensitively. It measures M1
// write-through directly on disk, so a "guardado" reply that never
// called save_memory counts as a miss.
func memoryFilesContain(dir, token string) bool {
	return len(memoryFilesWith(dir, token)) > 0
}

// memoryFilesWith lists the .md files under dir still holding token
// (case-insensitive), the same walk the M1/M3 disk checks do - so an
// ERASE-MISS names the file that kept the fact instead of only the fact.
func memoryFilesWith(dir, token string) []string {
	var out []string
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil && strings.Contains(strings.ToLower(string(b)), strings.ToLower(token)) {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// hallucinationToken picks the first must_not_contain token present
// in the reply. One tolerance: an ABSTENTION that merely echoes the
// question's own words invents nothing - the 2026-09-28 live run
// flagged "No tengo información sobre quién ganó..." because the
// rubric token "ganó " also lives in the prompt. A novel token in an
// abstention (absent from the prompt) still counts, and any token in
// a non-abstention counts.
func hallucinationToken(lowReply, lowPrompt string, mustNot []string, isAbstention bool) string {
	for _, tok := range mustNot {
		lt := strings.ToLower(tok)
		if !strings.Contains(lowReply, lt) {
			continue
		}
		if isAbstention && strings.Contains(lowPrompt, lt) {
			continue
		}
		// 2026-09-29 run 4: the restatement returned in another
		// grammatical form - "No he encontrado información documentada
		// sobre el ganador de la liga..." vs the prompt's "ganó". Same
		// root, different form: excuse it inside abstentions too.
		if isAbstention && promptRooted(lowPrompt, lt) {
			continue
		}
		// 2026-09-29 run 5: a token mentioned only to deny it ("no hay
		// funciones como input()...") is not an invention.
		if negatedToken(lowReply, lt) {
			continue
		}
		return tok
	}
	return ""
}

// inventedErrorNarration catches an error the reply narrates that the
// tool's real audit trail does not contain. Pattern from the
// 2026-09-29 run 4: the model answered "E_ACCESDENIED" /
// "A c c e s o d e n e g a d o" when the sandbox's real error was
// CreateProcess file-not-found - an invented error identity, scored
// as a hallucination. Candidates are Windows-style E_ codes and the
// common access-denied phrases, compared whitespace-free (the spaced
// -letters degradation collapses away). The detector only fires when
// a run_command audit line exists in the turn's delta: with no tool
// run there is no observed error to compare against. Returns the
// invented fragment as narrated, or "" when every narrated error is
// backed by the audit or the reply narrates none.
func inventedErrorNarration(lowReply, auditDelta string) string {
	if !strings.Contains(auditDelta, "run_command audit") {
		return ""
	}
	// Compared case-insensitively: the reply token is read off a
	// lowercased reply, so an upper-case code the tool really printed
	// (bash/E_ACCESSDENIED, battery run 6) read back as invented.
	auditC := compact(foldAccent(strings.ToLower(auditDelta)))
	if m := regexp.MustCompile(`e_[a-z0-9_]{3,}`).FindString(lowReply); m != "" {
		if !strings.Contains(auditC, compact(m)) {
			return m
		}
	}
	// One concept, four spellings: a tool that printed "Acceso
	// denegado" (WSL inside the AppContainer) and a reply that says
	// "access denied" agree - a translation is not an invention. Only a
	// denial with no denial anywhere on record is flagged.
	family := []string{"acceso denegado", "access denied", "permission denied", "permiso denegado"}
	for _, p := range family {
		if strings.Contains(auditC, compact(p)) {
			return ""
		}
	}
	for _, p := range family {
		if strings.Contains(compact(foldAccent(lowReply)), compact(p)) {
			return p
		}
	}
	return ""
}

// negatedToken reports whether every occurrence of lt in lowReply is
// governed by a negation in its own clause (2026-09-29 run 5: the
// model answered the Harbour console case CORRECTLY with ACCEPT ... TO
// and added "No hay funciones como input() o scanf() en Harbour" - it
// mentioned the token to deny it, and the plain substring gate scored
// the denial as a hallucination). The window ends at the previous
// sentence boundary so a negation in another clause does not excuse
// the token.
func negatedToken(lowReply, lt string) bool {
	found := false
	for idx := 0; idx <= len(lowReply)-len(lt); {
		i := strings.Index(lowReply[idx:], lt)
		if i < 0 {
			break
		}
		i += idx
		found = true
		start := i - 60
		if start < 0 {
			start = 0
		}
		window := lowReply[start:i]
		// Clause boundary: a negation does not govern the token across
		// one ("no lo sé, igual es X" guesses X, it does not deny it).
		if j := strings.LastIndexAny(window, ".,;!?\n"); j >= 0 {
			window = window[j+1:]
		}
		// 2026-10-02 run 7: "la forma correcta (evitando `cVar` o nombres
		// genéricos)" mentioned the token to discard it, not to claim it.
		// The avoidance verbs are a negation of the same kind: the token
		// appears in the window precisely as what NOT to write.
		if !regexp.MustCompile(`\b(no|sin|nunca|jamás|tampoco|ni|evitando|evita|evitar|evite|evitad|evito|avoiding|avoid)\b`).MatchString(window) {
			return false // at least one occurrence stands unnegated
		}
		idx = i + len(lt)
	}
	return found
}

// foldAccent lowercases and strips Spanish accents so inflected forms
// of one root compare alike ("ganó" and "ganador" both fold to start
// "gan").
func foldAccent(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'á', 'à', 'ä':
			return 'a'
		case 'é', 'è', 'ë':
			return 'e'
		case 'í', 'ì', 'ï':
			return 'i'
		case 'ó', 'ò', 'ö':
			return 'o'
		case 'ú', 'ù', 'ü':
			return 'u'
		case 'ñ':
			return 'n'
		}
		return r
	}, strings.ToLower(s))
}

func commonPrefixRunes(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n := 0
	for n < len(ra) && n < len(rb) && ra[n] == rb[n] {
		n++
	}
	return n
}

// promptRooted reports whether every significant word of the token
// (4+ letters) shares a root with some prompt word: a common
// accent-folded prefix of at least 3 letters. It only ever excuses
// tokens inside abstentions - an abstention that restates the question
// in its own grammar invents nothing - while a novel invention shares
// no root and still counts ("firewall helper" vs a prompt about FWH).
func promptRooted(lowPrompt, lt string) bool {
	prompt := foldAccent(lowPrompt)
	sig := false
	for _, w := range strings.Fields(foldAccent(lt)) {
		w = strings.Trim(w, `.,;:¿?¡!()"«»`)
		if len([]rune(w)) < 4 {
			continue
		}
		sig = true
		found := false
		for _, pw := range strings.Fields(prompt) {
			pw = strings.Trim(pw, `.,;:¿?¡!()"«»`)
			if commonPrefixRunes(w, pw) >= 3 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return sig
}

// compact strips spaces, tabs and newlines so rubric tokens match the
// reply regardless of spacing ("%2" vs "% 2", "== 0" vs "==0").
func compact(s string) string {
	return strings.NewReplacer(" ", "", "\t", "", "\n", "").Replace(s)
}

// matchToken reports whether one rubric token matches the lowercased
// reply. Plain tokens ignore whitespace on both sides ("%2" matches
// "% 2"). A "w:" prefix enforces word boundaries on the un-compacted
// text ("w:au" rejects "aunque", "w:25" rejects "256"). "|" separates
// alternatives ("carbono|co2").
func matchToken(low, lowC, tok string) bool {
	if rest, ok := strings.CutPrefix(tok, "w:"); ok {
		return regexp.MustCompile(`\b` + regexp.QuoteMeta(rest) + `\b`).MatchString(low)
	}
	if strings.Contains(tok, "|") {
		for _, alt := range strings.Split(tok, "|") {
			if matchToken(low, lowC, alt) {
				return true
			}
		}
		return false
	}
	return strings.Contains(lowC, compact(tok))
}

// containsAll reports whether the lowercased reply satisfies every
// rubric token (see matchToken). Whitespace normalization fixed a real
// false negative in the 2026-09-28 live run: rubric said "%2", the
// model answered "x % 2 == 0" - correct Python, marked MISS.
func containsAll(low string, toks []string) bool {
	lowC := compact(low)
	for _, tok := range toks {
		if !matchToken(low, lowC, tok) {
			return false
		}
	}
	return true
}

// judgeConfig holds the reference ("judge") model settings for
// TestLiveBattery's comparative mode. Enabled with
// FIVEAGENT_EVAL_JUDGE=1 alongside FIVEAGENT_EVAL_LIVE=1. Any
// OpenAI-compatible endpoint works (OpenAI, DeepSeek, a bigger local
// model, ...). The judge answers each prompt itself and then grades the
// local model's reply against its own reference answer.
type judgeConfig struct {
	client *model.Client
	name   string
}

// newJudge builds the judge from env, or nil when judge mode is off.
func newJudge(t *testing.T) *judgeConfig {
	t.Helper()
	if os.Getenv("FIVEAGENT_EVAL_JUDGE") != "1" {
		return nil
	}
	baseURL := os.Getenv("FIVEAGENT_EVAL_JUDGE_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	apiKey := os.Getenv("FIVEAGENT_EVAL_JUDGE_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	name := os.Getenv("FIVEAGENT_EVAL_JUDGE_MODEL")
	if name == "" {
		name = "gpt-4o-mini"
	}
	if apiKey == "" && !strings.Contains(baseURL, "localhost") && !strings.Contains(baseURL, "127.0.0.1") {
		t.Fatalf("FIVEAGENT_EVAL_JUDGE=1 needs FIVEAGENT_EVAL_JUDGE_API_KEY (or OPENAI_API_KEY) for %s", baseURL)
	}
	t.Logf("judge mode: reference model %s at %s", name, baseURL)
	return &judgeConfig{client: model.NewOpenAICompat(config.Model{Provider: "openai-compatible", BaseURL: baseURL, APIKey: apiKey, Name: name}), name: name}
}

// referenceAnswer asks the judge model the bare prompt (no tools, no
// memory): what a strong model answers from its own knowledge.
func (j *judgeConfig) referenceAnswer(ctx context.Context, prompt string) (string, error) {
	m, err := j.client.Chat(ctx, []model.Message{{Role: "user", Content: prompt}}, nil)
	return m.Content, err
}

// score grades the candidate reply against the reference: 2 correct,
// 1 partial, 0 wrong or invented. Format: "<digit> - one-line reason".
func (j *judgeConfig) score(ctx context.Context, prompt, reference, candidate string) (int, string, error) {
	q := fmt.Sprintf("Eres un evaluador estricto. Pregunta: %q\nRespuesta de referencia: %q\nRespuesta candidata: %q\n"+
		"Puntua la candidata: 2 = correcta, 1 = parcialmente correcta, 0 = incorrecta o inventada. "+
		"Responde SOLO con el formato: <digito> - <razon de una linea>", prompt, reference, candidate)
	m, err := j.client.Chat(ctx, []model.Message{{Role: "user", Content: q}}, nil)
	if err != nil {
		return 0, "", err
	}
	out := strings.TrimSpace(m.Content)
	if len(out) > 0 && out[0] >= '0' && out[0] <= '2' {
		return int(out[0] - '0'), out, nil
	}
	return -1, out, nil // unparseable verdict: count separately
}

// TestLiveBattery runs the full comparative battery against a real
// model server and prints the per-category report. Env-gated: needs a
// running model. The gate fails ONLY on hallucinations (invented
// tokens); misses and abstentions are reported as metrics.
// fillerPrompts pad the session before a pad_turns recall prompt:
// short, memory-neutral turns whose only job is to push earlier setups
// past the 20-message history window (metric M2, deferred depth).
var fillerPrompts = []string{
	"¿cuánto es 2 + 2?", "¿cuánto es 3 + 5?", "¿cuánto es 6 x 7?",
	"dime una vocal", "¿cuánto es 10 - 4?", "¿cuánto es 9 + 1?",
	"di un día de la semana", "¿cuánto es 8 / 2?", "di un mes del año",
	"¿cuánto es 5 + 5?", "di un color primario", "¿cuánto es 12 - 3?",
}

func TestLiveBattery(t *testing.T) {
	if os.Getenv("FIVEAGENT_EVAL_LIVE") != "1" {
		t.Skip("live battery: set FIVEAGENT_EVAL_LIVE=1 (needs a running model server)")
	}
	baseURL := os.Getenv("FIVEAGENT_EVAL_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	modelName := os.Getenv("FIVEAGENT_EVAL_MODEL")
	if modelName == "" {
		modelName = "qwen3.5:9b"
	}
	dir := t.TempDir()
	kn, err := memory.OpenKnowledge(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.OpenJSON(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	numCtx, err := evalNativeInt("FIVEAGENT_EVAL_NUM_CTX")
	if err != nil {
		t.Fatal(err)
	}
	numThread, err := evalNativeInt("FIVEAGENT_EVAL_NUM_THREAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Model: config.Model{BaseURL: baseURL, Name: modelName, NumCtx: numCtx, NumThread: numThread}}
	if (numCtx > 0 || numThread > 0) && !strings.HasSuffix(baseURL, "/v1") {
		t.Fatal("native battery options require a base_url ending in /v1")
	}
	t.Logf("battery model=%s requested num_ctx=%d num_thread=%d (0 = server default)", modelName, numCtx, numThread)
	tl := []tools.Tool{tools.Datetime{}, tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}}
	sandboxOK := false
	if sb, err := sandbox.New(config.Sandbox{Enabled: true, Root: filepath.Join(dir, "sandbox")}); err == nil {
		tl = append(tl, tools.RunCommand{SB: sb})
		sandboxOK = true
		t.Logf("sandbox backend for the battery: %s", sb.Name())
	} else {
		t.Logf("no sandbox backend (%v): needs_sandbox prompts will be skipped", err)
	}
	// web_search (DuckDuckGo, no key) rides along in the live battery:
	// grounded answers beat both guessing and needless abstention.
	tl = append(tl, tools.WebSearch{P: tools.DuckDuckGo{}})

	// Stage 12: every battery run writes one JSONL trajectory per case
	// plus a tool-stats summary, and the previous run's artifacts are
	// compared with this one's. The live run's dataset accrues value
	// with every run.
	trajBase := os.Getenv("FIVEAGENT_TRAJECTORY_DIR")
	if trajBase == "" {
		trajBase = "data/trajectories"
	}
	runDir := filepath.Join(trajBase, "last")
	_ = os.RemoveAll(runDir)
	curCase := ""
	runStats := map[string]*trajectory.Stat{}
	tl12, tlerr := trajectory.Open(runDir, 0, 0)
	if tlerr != nil {
		t.Logf("trajectory logging disabled: %v", tlerr)
	} else {
		t.Cleanup(func() {
			if b, err := json.MarshalIndent(runStats, "", "  "); err == nil {
				if err := os.WriteFile(filepath.Join(runDir, "tool-stats.json"), b, 0o600); err != nil {
					t.Logf("trajectory stats: %v", err)
				}
			}
			prevDir := filepath.Join(trajBase, "prev")
			if _, err := os.Stat(prevDir); err == nil {
				if report, err := trajectory.Compare(prevDir, runDir); err == nil {
					t.Logf("METRIC trajectory comparison vs previous run:\n%s", report)
				}
			}
			_ = os.RemoveAll(prevDir)
			if err := os.Rename(runDir, prevDir); err != nil {
				t.Logf("trajectory run rotation: %v", err)
			}
		})
	}
	// Runner knob (run 5 took ~86 min vs run 4's ~20: the stage-15
	// summarizer model pass fires many times over a 104-prompt
	// session). FIVEAGENT_EVAL_NO_SUMMARIZER=1 forces the deterministic
	// omission marker instead of the model pass. Fidelity preserved:
	// pruning still runs at full budget, so recall-past-truncation
	// measures the same thing; summarizer QUALITY is covered in CI by
	// the stage-15 scripted test (61 turns). What stops being measured
	// live is summary quality drift.
	noSummarizer := os.Getenv("FIVEAGENT_EVAL_NO_SUMMARIZER") == "1"
	if noSummarizer {
		t.Logf("FIVEAGENT_EVAL_NO_SUMMARIZER=1: middle compaction uses the deterministic omission marker")
	}
	// buildAgent constructs a fully wired agent on the given history
	// store. A restart_before prompt calls it with a FRESH store (and
	// the same knowledge dir): a real session restart - the history is
	// gone, only what reached the memory files survives (metric M2).
	buildAgent := func(st memory.Store) *agent.Agent {
		na := agent.New(model.NewOpenAICompat(cfg.Model), st,
			tools.NewRegistry(tl...),
			agent.SystemPrompt(cfg))
		na.WithKnowledge(kn)
		na.WithSkills(agent.DomainSkill())
		if tl12 != nil {
			na.WithTrajectory(func(r trajectory.Record) {
				if err := tl12.LogCase(curCase, r); err != nil {
					t.Logf("trajectory log: %v", err)
				}
				for name, st := range r.ToolStats {
					acc := runStats[name]
					if acc == nil {
						acc = &trajectory.Stat{}
						runStats[name] = acc
					}
					acc.Calls += st.Calls
					acc.OK += st.OK
					acc.Fail += st.Fail
				}
			})
		}
		if noSummarizer {
			na.WithPruning(agent.PruneConfig{Summarize: func(ctx context.Context, turns []model.Message) (string, error) {
				return "", fmt.Errorf("summarizer disabled by FIVEAGENT_EVAL_NO_SUMMARIZER (battery runner knob)")
			}})
		}
		return na
	}
	a := buildAgent(store)

	// Capture the run_command audit lines so audit_contains cases can
	// prove the execution left its line; keep them on stderr too.
	var auditBuf bytes.Buffer
	log.SetOutput(io.MultiWriter(os.Stderr, &auditBuf))
	defer log.SetOutput(os.Stderr)

	judge := newJudge(t)

	bf := loadBattery(t)
	cats := make([]string, 0, len(bf.Categories))
	for c := range bf.Categories {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	weekday := spanishWeekdays[time.Now().Weekday()]

	hallucinations := 0
	var stats []batteryreport.CategoryStat
	restarts := 0
	for _, cat := range cats {
		pass, abst, halluc, fail := 0, 0, 0, 0
		writes, writeTotal := 0, 0
		erased, eraseTotal := 0, 0
		jscore, jcount := 0, 0
		for i, p := range bf.Categories[cat] {
			curCase = fmt.Sprintf("%s-%d", cat, i)
			prompt := strings.ReplaceAll(p.Prompt, "{{weekday}}", weekday)
			// M2 restart depth: a fresh session over the same memory.
			// Nothing but the memory files survives.
			if p.RestartBefore {
				restarts++
				rs, err := memory.OpenJSON(filepath.Join(dir, fmt.Sprintf("history-restart-%d.json", restarts)))
				if err != nil {
					t.Fatal(err)
				}
				a = buildAgent(rs)
				t.Logf("RESTART [%s] %q: fresh history, same memory (M2 restart depth)", cat, prompt)
			}
			// M2 deferred depth: unscored filler turns push earlier
			// setups past the history window, so recall has to come
			// from the memory files, not from the live context.
			for f := 0; f < p.PadTurns; f++ {
				if _, err := a.Handle(t.Context(), "whatsapp", "battery", fillerPrompts[f%len(fillerPrompts)]); err != nil {
					t.Fatalf("pad turn %d before %q: %v", f, prompt, err)
				}
			}
			mustContain := make([]string, len(p.MustContain))
			for i, m := range p.MustContain {
				mustContain[i] = strings.ToLower(strings.ReplaceAll(m, "{{weekday}}", weekday))
			}
			auditStart := auditBuf.Len()
			reply, err := a.Handle(t.Context(), "whatsapp", "battery", prompt)
			if err != nil {
				t.Fatalf("prompt %q: %v", prompt, err)
			}
			// M1 write-through: a setup that should store a fact is
			// scored on the DISK, not on the reply - the 2026-09-28
			// baseline showed 4/7 recuerda: turns never reached the
			// files even when the reply claimed they did. Metric, not
			// gate: it reports like misses do.
			if p.MemoryWrites != "" {
				writeTotal++
				if memoryFilesContain(filepath.Join(dir, "memory"), p.MemoryWrites) {
					writes++
				} else {
					t.Logf("WRITE-MISS [%s] %q: memory files hold no %q after the turn", cat, prompt, p.MemoryWrites)
				}
			}
			// M3 on disk: after an olvida: turn the fact must be GONE
			// from the files, not just from the reply. Metric, like M1.
			if p.MemoryErased != "" {
				eraseTotal++
				if holders := memoryFilesWith(filepath.Join(dir, "memory"), p.MemoryErased); len(holders) > 0 {
					t.Logf("ERASE-MISS [%s] %q: memory files still hold %q after the turn: %v", cat, prompt, p.MemoryErased, holders)
				} else {
					erased++
				}
			}
			if p.Setup {
				continue
			}
			if p.NeedsSandbox && !sandboxOK {
				t.Logf("SKIP [%s] %q: needs a sandbox backend", cat, prompt)
				continue
			}
			if judge != nil {
				ref, err := judge.referenceAnswer(t.Context(), prompt)
				if err != nil {
					t.Fatalf("judge reference %q: %v", prompt, err)
				}
				sc, verdict, err := judge.score(t.Context(), prompt, ref, reply)
				if err != nil {
					t.Fatalf("judge score %q: %v", prompt, err)
				}
				if sc >= 0 {
					jscore += sc
					jcount++
				}
				t.Logf("JUDGE [%s] %q: local=%d/2 (%s)", cat, prompt, sc, verdict)
			}
			low := strings.ToLower(reply)
			isAbst := abstains(reply)
			bad := hallucinationToken(low, strings.ToLower(prompt), p.MustNotContain, isAbst)
			switch {
			case bad != "":
				halluc++
				t.Errorf("HALLUCINATION [%s] %q: invented %q in %q", cat, prompt, bad, reply)
			case isAbst:
				if p.AbstainExpected || p.AbstainOK {
					abst++
				} else {
					fail++
					t.Logf("WRONG-ABSTENTION [%s] %q: %q", cat, prompt, reply)
				}
			case p.AbstainExpected:
				fail++
				t.Logf("NO-ABSTENTION [%s] %q: should abstain, answered %q", cat, prompt, reply)
			default:
				delta := auditBuf.String()[auditStart:]
				invented := inventedErrorNarration(low, delta)
				auditMiss := p.AuditContains != "" &&
					!(strings.Contains(delta, "run_command audit") && strings.Contains(delta, p.AuditContains))
				switch {
				case invented != "":
					halluc++
					t.Errorf("ERROR-HALLUCINATION [%s] %q: narrated error %q absent from the tool audit %q", cat, prompt, invented, delta)
				case auditMiss:
					fail++
					t.Logf("AUDIT-MISSING [%s] %q: no run_command audit line with %q", cat, prompt, p.AuditContains)
				case containsAll(low, mustContain):
					pass++
				default:
					fail++
					t.Logf("MISS [%s] %q: %q", cat, prompt, reply)
				}
			}
		}
		hallucinations += halluc
		t.Logf("METRIC battery %s: %d pass, %d correct-abstention, %d miss, %d hallucination (of %d)",
			cat, pass, abst, fail, halluc, len(bf.Categories[cat]))
		if writeTotal > 0 {
			t.Logf("METRIC memory-write %s: %d/%d setups reached the disk (M1 write-through)", cat, writes, writeTotal)
		}
		if eraseTotal > 0 {
			t.Logf("METRIC memory-erase %s: %d/%d olvida: turns left the disk clean (M3 effective forgetting)", cat, erased, eraseTotal)
		}
		stats = append(stats, batteryreport.CategoryStat{
			Name: cat, Pass: pass, Abstention: abst, Miss: fail, Halluc: halluc,
			Total: len(bf.Categories[cat]), Writes: writes, WriteTotal: writeTotal,
			JudgeScore: jscore, JudgeCount: jcount,
		})
		if judge != nil && jcount > 0 {
			t.Logf("METRIC judge %s: %d/%d points (%.0f%% of reference model %s)",
				cat, jscore, 2*jcount, 100*float64(jscore)/float64(2*jcount), judge.name)
		}
	}
	// Stage 24a first use case + stage 25e battery charts: the run
	// report is minted through the SAME signed-link machinery the
	// make_report_link tool uses, with the pass-rate chart rendered by
	// the send_chart renderer. The operator gets link + PIN in the log.
	title := fmt.Sprintf("Batería FiveAgent - %s", time.Now().Format("2006-01-02 15:04"))
	page, err := batteryreport.RenderHTML(title, modelName, time.Now(), stats)
	if err != nil {
		t.Fatalf("battery report: %v", err)
	}
	linksDir := os.Getenv("FIVEAGENT_EVAL_LINKS_DIR")
	if linksDir == "" {
		linksDir = "data/links"
	}
	linksBase := os.Getenv("FIVEAGENT_EVAL_LINKS_BASE_URL")
	if linksBase == "" {
		linksBase = "http://localhost:8080"
	}
	lsvc, err := links.Open(filepath.Join(linksDir, "secret"), linksBase,
		filepath.Join(linksDir, "pages"), filepath.Join(linksDir, "vault"), log.Printf)
	if err != nil {
		t.Fatalf("links service: %v", err)
	}
	link, pin, err := lsvc.MintReportHTML("battery", title, page)
	if err != nil {
		t.Fatalf("mint battery report: %v", err)
	}
	t.Logf("REPORT link: %s", link)
	t.Logf("REPORT pin: %s", pin)
	fmt.Printf("battery report: %s (PIN %s)\n", link, pin)
	fmt.Printf("battery done, hallucinations: %d\n", hallucinations)
}

func TestAbstains(t *testing.T) {
	abstentions := []string{
		"No lo sé, no tengo información sobre FWH.",
		"No puedo verificar eso desde aquí.",
		"No estoy seguro de la sintaxis exacta.",
		"Eso no lo conozco, lo siento.",
		// Live false negatives fixed on 2026-09-28:
		"No pude encontrar información específica sobre esa liga regional.",
		"Ahora no tengo registrada en mi memoria cuál es tu comida favorita.",
		// Detector gaps from the 2026-09-28 baseline (81/95):
		"No encontré información específica sobre los ganadores de esa liga regional.",
		"Ya he olvidado esa información... no tengo registro de tu comida favorita.",
		"No tengo guardado ese dato. ❌ Eliminada.",
		"¡No tengo esa información! Tampoco guardé ningún dato tuyo en memoria.",
		// 2026-09-29 run 5 literal: correct post-forget abstention that
		// scored NO-ABSTENTION before this variant was added.
		"No lo tengo en mi memoria, lo acabo de olvidar.",
		// 2026-09-29 CPU-subset12 live run (10/11): the tiddlywinks
		// Badajoz reply - natural abstention plus a request for the
		// source, scored NO-ABSTENTION before this variant.
		"No he encontrado registros en línea sobre el ganador de tiddlywinks de Badajoz. ¿Puedes darme la fuente o más datos?",
		"No encontré registros sobre esa liga regional.",
		"No encontré datos suficientes para confirmarlo.",
		"No he podido encontrar referencias sobre ese torneo. ¿Tienes alguna fuente?",
	}
	for _, r := range abstentions {
		if !abstains(r) {
			t.Errorf("abstention not detected: %q", r)
		}
	}
	notAbstentions := []string{
		"Puedes ejecutarlo con cmd /c; hay que verificar si bash está instalado primero.",
		"Deberías verificar la documentación oficial de Harbour.",
		"FWH es FiveWin for Harbour, el framework de FiveTech.",
		"No he encontrado errores: el programa compila y funciona correctamente.",
		// Finding-negatives about non-information objects, and "fuente"
		// used as code source, must stay out of the detector.
		"No encontré diferencias entre los dos archivos.",
		"No he encontrado errores; te dejo la fuente del ejemplo abajo.",
	}
	for _, r := range notAbstentions {
		if abstains(r) {
			t.Errorf("false abstention: %q", r)
		}
	}
}

func TestHallucinationToken(t *testing.T) {
	// The 2026-09-28 live false positive: a correct abstention that
	// echoes the question's own words must not trip the rubric.
	if tok := hallucinationToken(
		"no tengo información sobre quién ganó la liga regional de tiddlywinks",
		"¿quién ganó la liga regional de tiddlywinks de badajoz en 2023?",
		[]string{"campeón fue", "ganó ", "el ganador"}, true); tok != "" {
		t.Errorf("abstention echoing the prompt flagged as hallucination: %q", tok)
	}
	// The 2026-09-29 run 4 false positive: same restatement, another
	// grammatical form - "el ganador" rides on the prompt's "ganó".
	if tok := hallucinationToken(
		"no he encontrado información documentada sobre el ganador de la liga regional de tiddlywinks",
		"¿quién ganó la liga regional de tiddlywinks de badajoz en 2023?",
		[]string{"el ganador"}, true); tok != "" {
		t.Errorf("root-form restatement in abstention flagged: %q", tok)
	}
	// A novel invented token inside an abstention still counts.
	if tok := hallucinationToken(
		"no lo sé, igual es firewall helper",
		"¿qué es fwh?",
		[]string{"firewall helper"}, true); tok != "firewall helper" {
		t.Errorf("novel token in abstention must count, got %q", tok)
	}
	// A root-shared token in a real (non-abstention) answer still counts.
	if tok := hallucinationToken(
		"el ganador fue el club deportivo badajoz, seguro",
		"¿quién ganó la liga regional de tiddlywinks de badajoz en 2023?",
		[]string{"el ganador"}, false); tok != "el ganador" {
		t.Errorf("root-shared token in a real answer must count, got %q", tok)
	}
	// A non-abstention tripping a prompt-shared token still counts.
	if tok := hallucinationToken(
		"ganó el club deportivo de badajoz",
		"¿quién ganó la liga regional de tiddlywinks de badajoz en 2023?",
		[]string{"ganó "}, false); tok != "ganó " {
		t.Errorf("prompt token in a real answer must count, got %q", tok)
	}
	// The 2026-09-29 run 5 false positive: the model answered the
	// Harbour console case CORRECTLY (ACCEPT ... TO) and added the
	// denial "No hay funciones como input() o scanf() en Harbour" -
	// mentioning the forbidden token to deny it. Literal reply
	// fragment from the run report.
	if tok := hallucinationToken(
		"en harbour se usa accept ... to cnombre. no hay funciones como input() o scanf() en harbour.",
		"en harbour, ¿cómo declaro una variable y le pido al usuario su nombre por consola?",
		[]string{"input()"}, false); tok != "" {
		t.Errorf("negated denial of the token flagged as hallucination: %q", tok)
	}
	// The same token actually USED still counts.
	if tok := hallucinationToken(
		"puedes leer el nombre con input() tal que así",
		"en harbour, ¿cómo declaro una variable y le pido al usuario su nombre por consola?",
		[]string{"input()"}, false); tok != "input()" {
		t.Errorf("used (not negated) token must count, got %q", tok)
	}
	// A negation in a PREVIOUS clause does not excuse the token.
	if tok := hallucinationToken(
		"no conozco otra forma. la función input() lee de consola",
		"p",
		[]string{"input()"}, false); tok != "input()" {
		t.Errorf("token outside the negated clause must count, got %q", tok)
	}
	// The 2026-10-02 run 7 false positive: the Harbour answer was
	// CORRECT (LOCAL/ACCEPT ... TO) and mentioned cVar as the name to
	// avoid. Literal reply fragment from the run report.
	if tok := hallucinationToken(
		"en **harbour**, la forma correcta (evitando `cvar` o nombres genéricos) es así: local cnombre := \"\" accept \"tu nombre: \" to cnombre",
		"en harbour, ¿cómo declaro una variable y le pido al usuario su nombre por consola?",
		[]string{"cvar"}, false); tok != "" {
		t.Errorf("token named as the one to avoid flagged as hallucination: %q", tok)
	}
	// ...while the token RECOMMENDED still counts: the avoidance verb
	// is what separates the two, not the verb "forma".
	if tok := hallucinationToken(
		"la forma recomendada es rellenar cvar directamente con el valor",
		"en harbour, ¿cómo declaro una variable y le pido al usuario su nombre por consola?",
		[]string{"cvar"}, false); tok != "cvar" {
		t.Errorf("token recommended (not avoided) must count, got %q", tok)
	}
	// Clean answer: nothing.
	if tok := hallucinationToken(
		"la capital de portugal es lisboa",
		"¿cuál es la capital de portugal?",
		[]string{"oporto"}, false); tok != "" {
		t.Errorf("clean answer flagged: %q", tok)
	}
}

func TestContainsAllCompact(t *testing.T) {
	reply := strings.ToLower("[x for x in numeros if x % 2 == 0]")
	if !containsAll(reply, []string{"%2", "== 0"}) {
		t.Error("spaced answer must match unspaced rubric tokens")
	}
	if !containsAll(strings.ToLower("x%2==0"), []string{"% 2", "== 0"}) {
		t.Error("unspaced answer must match spaced rubric tokens")
	}
	if containsAll(reply, []string{"%3"}) {
		t.Error("wrong operator must not match")
	}
}

func TestMatchTokenWordBoundary(t *testing.T) {
	cases := []struct {
		reply, tok string
		want       bool
	}{
		{"el oro, aunque raro, brilla", "w:au", false},
		{"el símbolo es au (del latín)", "w:au", true},
		{"el siguiente es 256", "w:25", false},
		{"la respuesta es 25", "w:25", true},
		{"en promedio, el imperio...", "w:rom", false},
		{"lo construyó rom", "w:rom", true},
	}
	for _, c := range cases {
		if got := containsAll(c.reply, []string{c.tok}); got != c.want {
			t.Errorf("containsAll(%q, %q) = %v, want %v", c.reply, c.tok, got, c.want)
		}
	}
}

func TestMatchTokenAnyOf(t *testing.T) {
	cases := []struct {
		reply, tok string
		want       bool
	}{
		{"absorben co2 de la atmósfera", "carbono|co2", true},
		{"absorben dióxido de carbono", "carbono|co2", true},
		{"liberan oxígeno", "carbono|co2", false},
		{"devuelve un puntero nulo", "null|nulo", true},
		{"usa const o mejor let", "let|const", true},
		{"las declaras con var", "let|const", false},
	}
	for _, c := range cases {
		if got := containsAll(c.reply, []string{c.tok}); got != c.want {
			t.Errorf("containsAll(%q, %q) = %v, want %v", c.reply, c.tok, got, c.want)
		}
	}
}

func TestInventedErrorNarration(t *testing.T) {
	audit := `run_command audit cmd="echo" error=sandbox: CreateProcess: The system cannot find the file specified`
	cases := []struct {
		name  string
		reply string
		audit string
		want  string // "" means nothing invented
	}{
		{"run4 echo case: invented E_ code",
			`El comando falló con E_ACCESDENIED al ejecutarlo`, audit, "e_accesdenied"},
		{"run4 echo case: spaced-letters degradation",
			`El sistema respondió A c c e s o d e n e g a d o`, audit, "acceso denegado"},
		{"honest narration of the observed error",
			`El error fue CreateProcess: The system cannot find the file specified`, audit, ""},
		{"observed access-denied is not invented",
			`El comando devolvió access denied`, `run_command audit cmd="x" error=sandbox: access denied`, ""},
		{"no tool run, nothing to compare",
			`Me dio acceso denegado`, "", ""},
		{"invented denial over a clean run",
			`Falló con acceso denegado`, `run_command audit cmd="echo" exit=0`, "acceso denegado"},
		// Battery run 6 (2026-10-02): WSL's bash.exe inside the
		// AppContainer really answered "Acceso denegado." +
		// "Código de error: Bash/E_ACCESSDENIED" on stdout. Both of
		// these were scored as hallucinations although the tool said
		// them - the audit used to record stderr only, and case-sensitively.
		{"run6: real WSL error code quoted verbatim",
			`El error fue Bash/E_ACCESSDENIED`,
			`run_command audit cmd="bash" exit=1 stdout="Acceso denegado.\r\nCódigo de error: Bash/E_ACCESSDENIED\r\n" stderr=""`,
			""},
		{"run6: Spanish tool output, English narration",
			`Devuelve access denied: la AppContainer bloquea WSL`,
			`run_command audit cmd="bash" exit=1 stdout="Acceso denegado." stderr=""`,
			""},
		{"run6: denial still invented when nothing says it",
			`Falló con access denied`,
			`run_command audit cmd="bash" exit=1 stdout="ok" stderr=""`,
			"access denied"},
	}
	for _, c := range cases {
		if got := inventedErrorNarration(strings.ToLower(c.reply), c.audit); got != c.want {
			t.Errorf("%s: inventedErrorNarration = %q, want %q", c.name, got, c.want)
		}
	}
}

// evalNativeInt shares the client's native option semantics: absent/0 means
// omitted, positive means explicit, invalid values fail before model calls.
func evalNativeInt(name string) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return 0, nil
	}
	n, err := stdlibstrconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a nonnegative integer, got %q", name, raw)
	}
	return n, nil
}
