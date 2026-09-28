package evals

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	Setup           bool     `yaml:"setup"`         // conversation setup turn, no scoring
	NeedsSandbox    bool     `yaml:"needs_sandbox"` // needs a run_command backend; skipped when unavailable
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
			if len(p.MustContain)+len(p.MustNotContain) == 0 && !p.AbstainOK && !p.AbstainExpected && !p.Setup {
				t.Errorf("%s[%d]: no expectation (must_contain, must_not_contain, abstain_*, setup)", cat, i)
			}
		}
		total += len(ps)
	}
	t.Logf("METRIC battery file: %d prompts in %d categories, all with expectations", total, len(bf.Categories))
}

var spanishWeekdays = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// abstains reports whether the reply is an honest "I don't know".
func abstains(reply string) bool {
	low := strings.ToLower(reply)
	for _, m := range []string{"no lo sé", "no sé", "no estoy seguro", "no tengo información", "no puedo saber", "verificar", "no lo conozco", "no tengo constancia"} {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
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
	a := agent.New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tl...),
		agent.SystemPrompt(cfg))
	a.WithKnowledge(kn)

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
		jscore, jcount := 0, 0
		for _, p := range bf.Categories[cat] {
			prompt := strings.ReplaceAll(p.Prompt, "{{weekday}}", weekday)
			mustContain := make([]string, len(p.MustContain))
			for i, m := range p.MustContain {
				mustContain[i] = strings.ToLower(strings.ReplaceAll(m, "{{weekday}}", weekday))
			}
			reply, err := a.Handle(t.Context(), "whatsapp", "battery", prompt)
			if err != nil {
				t.Fatalf("prompt %q: %v", prompt, err)
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
			bad := ""
			for _, tok := range p.MustNotContain {
				if strings.Contains(low, strings.ToLower(tok)) {
					bad = tok
					break
				}
			}
			switch {
			case bad != "":
				halluc++
				t.Errorf("HALLUCINATION [%s] %q: invented %q in %q", cat, prompt, bad, reply)
			case abstains(reply):
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
				ok := true
				for _, m := range mustContain {
					if !strings.Contains(low, m) {
						ok = false
						break
					}
				}
				if ok {
					pass++
				} else {
					fail++
					t.Logf("MISS [%s] %q: %q", cat, prompt, reply)
				}
			}
		}
		hallucinations += halluc
		t.Logf("METRIC battery %s: %d pass, %d correct-abstention, %d miss, %d hallucination (of %d)",
			cat, pass, abst, fail, halluc, len(bf.Categories[cat]))
		if judge != nil && jcount > 0 {
			t.Logf("METRIC judge %s: %d/%d points (%.0f%% of reference model %s)",
				cat, jscore, 2*jcount, 100*float64(jscore)/float64(2*jcount), judge.name)
		}
	}
	fmt.Printf("battery done, hallucinations: %d\n", hallucinations)
}
