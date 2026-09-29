package evals

// skills_test.go - stage 32 done-when: the battery runs the same
// harbour_fivewin prompts with and without the matching skill loaded
// and reports the delta; the domain answers pass ONLY with the skill.
//
// The scripted player answers by request content: when the turn
// carries the domain reference (the skills/ body injected on a
// trigger match) it answers with the verified facts; when the
// reference is absent it confabulates exactly the way the small model
// did live (FWH = "FireWall Helper", cVar, Input()). A pass with the
// skill and a fail without it proves the wiring carries the skill
// and the skill carries the facts - a pass on both sides would fake
// the measurement.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func TestSkillDeltaHarbourFivewin(t *testing.T) {
	const (
		referenceMarker = "FWH means FiveWin for Harbour"
		grounded        = `FWH es FiveWin for Harbour, el framework de FiveTech. En Harbour: LOCAL cNombre := \"\"; ACCEPT \"tu nombre: \" TO cNombre. En FiveWin: #include FiveWin.ch, DEFINE WINDOW oWnd y ACTIVATE WINDOW oWnd.`
		confabulated    = `FWH es FireWall Helper. En Harbour declaras cVar := Input() para leer el nombre.`
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(b), referenceMarker) {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"`+grounded+`"}}]}`)
		} else {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"`+confabulated+`"}}]}`)
		}
	}))
	t.Cleanup(srv.Close)

	newAgent := func(withSkills bool) *agent.Agent {
		store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
		if err != nil {
			t.Fatal(err)
		}
		cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
		a := agent.New(model.NewOpenAICompat(cfg), store, tools.NewRegistry(), agent.SystemPrompt(&config.Config{}))
		if withSkills {
			a.WithSkills(agent.LoadSkillsDir(agent.DefaultSkillsDir)...)
		}
		return a
	}

	bf := loadBattery(t)
	prompts := bf.Categories["harbour_fivewin"]
	if len(prompts) == 0 {
		t.Fatal("battery lost its harbour_fivewin category")
	}
	passWith, passWithout := 0, 0
	for _, p := range prompts {
		must := make([]string, len(p.MustContain))
		for i, m := range p.MustContain {
			must[i] = strings.ToLower(m)
		}
		for _, withSkills := range []bool{true, false} {
			reply, err := newAgent(withSkills).Handle(context.Background(), "whatsapp", "u1", p.Prompt)
			if err != nil {
				t.Fatalf("prompt %q (skills=%v): %v", p.Prompt, withSkills, err)
			}
			low := strings.ToLower(reply)
			pass := containsAll(low, must) && hallucinationToken(low, strings.ToLower(p.Prompt), p.MustNotContain, false) == ""
			if withSkills && pass {
				passWith++
			}
			if !withSkills && pass {
				passWithout++
			}
		}
	}
	t.Logf("METRIC skills delta harbour_fivewin: %d/%d pass with the skill, %d/%d without",
		passWith, len(prompts), passWithout, len(prompts))
	if passWith != len(prompts) {
		t.Errorf("every domain answer must pass with the skill loaded: %d/%d", passWith, len(prompts))
	}
	if passWithout != 0 {
		t.Errorf("no domain answer may pass without the skill (the player confabulates): %d/%d passed", passWithout, len(prompts))
	}
}
