package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

const sampleSkill = `# Sample skill

name: sample
trigger: one line about the sample skill
keywords: alpha, beta
tools: current_datetime

Body line one.
Body line two.
`

func TestParseSkillMD(t *testing.T) {
	sk, err := ParseSkillMD(sampleSkill)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "sample" || sk.TriggerLine != "one line about the sample skill" {
		t.Errorf("header fields wrong: %+v", sk)
	}
	if len(sk.Triggers) != 2 || sk.Triggers[0] != "alpha" || sk.Triggers[1] != "beta" {
		t.Errorf("keywords wrong: %v", sk.Triggers)
	}
	if len(sk.Tools) != 1 || sk.Tools[0] != "current_datetime" {
		t.Errorf("tools wrong: %v", sk.Tools)
	}
	if body := sk.Load(); !strings.Contains(body, "Body line one.") || !strings.Contains(body, "Body line two.") {
		t.Errorf("body wrong: %q", body)
	}
}

func TestParseSkillMDDefaultsKeywordsToName(t *testing.T) {
	sk, err := ParseSkillMD("name: solo\ntrigger: t\n\nbody")
	if err != nil {
		t.Fatal(err)
	}
	if len(sk.Triggers) != 1 || sk.Triggers[0] != "solo" {
		t.Errorf("keywords must default to the name, got %v", sk.Triggers)
	}
}

func TestParseSkillMDRejects(t *testing.T) {
	cases := map[string]string{
		"missing name":    "trigger: t\n\nbody",
		"missing trigger": "name: x\n\nbody",
		"empty body":      "name: x\ntrigger: t\n",
		"unknown key":     "name: x\ntrigger: t\nweight: 3\n\nbody",
		"malformed line":  "name: x\nnot-a-header\n\nbody",
	}
	for label, text := range cases {
		if _, err := ParseSkillMD(text); err == nil {
			t.Errorf("%s: must be rejected", label)
		}
	}
}

func TestLoadSkillsDirPrefersDiskAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// No skills on disk: the embedded fivetech-domain must ride.
	skills := LoadSkillsDir(DefaultSkillsDir)
	found := false
	for _, sk := range skills {
		if sk.Name == "fivetech-domain" {
			found = true
		}
	}
	if !found {
		t.Fatal("embedded fivetech-domain must load when the disk has no skills/")
	}
	// A disk skill with the same name shadows the embedded copy.
	p := filepath.Join(dir, DefaultSkillsDir, "fivetech")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("name: fivetech-domain\ntrigger: t\n\nDISK-MARKER"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sk := range LoadSkillsDir(DefaultSkillsDir) {
		if sk.Name == "fivetech-domain" && !strings.Contains(sk.Load(), "DISK-MARKER") {
			t.Error("the on-disk skill must shadow the embedded copy")
		}
	}
}

func TestSkillsIndex(t *testing.T) {
	if SkillsIndex(nil) != "" {
		t.Error("no skills, no index")
	}
	idx := SkillsIndex([]Skill{{Name: "a", TriggerLine: "first"}, {Name: "b", TriggerLine: "second"}})
	if !strings.Contains(idx, "- a: first") || !strings.Contains(idx, "- b: second") {
		t.Errorf("index must carry one line per skill: %q", idx)
	}
	if strings.Contains(idx, "Body line") {
		t.Error("the index must never carry skill bodies")
	}
}

func TestSkillGatedToolsRideOnlyOnTrigger(t *testing.T) {
	a := New(nil, &fakeStore{}, tools.NewRegistry(tools.Datetime{}), "sys")
	a.skills = []Skill{{
		Name:        "gater",
		TriggerLine: "t",
		Triggers:    []string{"magicword"},
		Tools:       []string{"current_datetime"},
		Load:        func() string { return "body" },
	}}
	for _, sp := range a.toolSpecsFor(map[string]bool{}) {
		if sp.Function.Name == "current_datetime" {
			t.Error("a skill-gated tool must not be offered before the skill triggers")
		}
	}
	found := false
	for _, sp := range a.toolSpecsFor(map[string]bool{"gater": true}) {
		if sp.Function.Name == "current_datetime" {
			found = true
		}
	}
	if !found {
		t.Error("a skill-gated tool must be offered once its skill triggered")
	}
}
