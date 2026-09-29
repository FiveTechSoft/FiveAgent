package evals

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
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
			if len(p.MustContain)+len(p.MustNotContain) == 0 && !p.AbstainOK && !p.AbstainExpected && !p.Setup && p.AuditContains == "" && p.MemoryWrites == "" {
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
func abstains(reply string) bool {
	low := strings.ToLower(reply)
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
	found := false
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err == nil && strings.Contains(strings.ToLower(string(b)), strings.ToLower(token)) {
			found = true
		}
		return nil
	})
	return found
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
	auditC := compact(foldAccent(auditDelta))
	if m := regexp.MustCompile(`e_[a-z0-9_]{3,}`).FindString(lowReply); m != "" {
		if !strings.Contains(auditC, compact(m)) {
			return m
		}
	}
	for _, phrase := range []string{"acceso denegado", "access denied", "permission denied", "permiso denegado"} {
		if strings.Contains(compact(foldAccent(lowReply)), compact(phrase)) && !strings.Contains(auditC, compact(phrase)) {
			return phrase
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
		if !regexp.MustCompile(`\b(no|sin|nunca|jamás|tampoco|ni)\b`).MatchString(window) {
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
	cfg := &config.Config{Model: config.Model{BaseURL: baseURL, Name: modelName}}
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

	a := agent.New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tl...),
		agent.SystemPrompt(cfg))
	a.WithKnowledge(kn)
	a.WithSkills(agent.DomainSkill())
	// Runner knob (run 5 took ~86 min vs run 4's ~20: the stage-15
	// summarizer model pass fires many times over a 104-prompt
	// session). FIVEAGENT_EVAL_NO_SUMMARIZER=1 forces the deterministic
	// omission marker instead of the model pass. Fidelity preserved:
	// pruning still runs at full budget, so recall-past-truncation
	// measures the same thing; summarizer QUALITY is covered in CI by
	// the stage-15 scripted test (61 turns). What stops being measured
	// live is summary quality drift.
	if os.Getenv("FIVEAGENT_EVAL_NO_SUMMARIZER") == "1" {
		a.WithPruning(agent.PruneConfig{Summarize: func(ctx context.Context, turns []model.Message) (string, error) {
			return "", fmt.Errorf("summarizer disabled by FIVEAGENT_EVAL_NO_SUMMARIZER (battery runner knob)")
		}})
		t.Logf("FIVEAGENT_EVAL_NO_SUMMARIZER=1: middle compaction uses the deterministic omission marker")
	}

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
	for _, cat := range cats {
		pass, abst, halluc, fail := 0, 0, 0, 0
		writes, writeTotal := 0, 0
		jscore, jcount := 0, 0
		for _, p := range bf.Categories[cat] {
			prompt := strings.ReplaceAll(p.Prompt, "{{weekday}}", weekday)
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
		if judge != nil && jcount > 0 {
			t.Logf("METRIC judge %s: %d/%d points (%.0f%% of reference model %s)",
				cat, jscore, 2*jcount, 100*float64(jscore)/float64(2*jcount), judge.name)
		}
	}
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
	}
	for _, c := range cases {
		if got := inventedErrorNarration(strings.ToLower(c.reply), c.audit); got != c.want {
			t.Errorf("%s: inventedErrorNarration = %q, want %q", c.name, got, c.want)
		}
	}
}
