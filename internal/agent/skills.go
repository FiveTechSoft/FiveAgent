package agent

// skills.go - the skills/ folder format (stage 32 of docs/ROADMAP.md).
//
// One SKILL.md per skill, in its own subfolder:
//
//	# Human title (cosmetic)
//
//	name: fivetech-domain
//	trigger: one line describing what the skill is for; this line
//	         rides in the system prompt index on every turn.
//	keywords: comma, separated, match, words (default: the name)
//	tools: comma, separated, tool names (optional; offered only on
//	       turns where this skill triggered)
//
//	Full procedure text, written for a small model.
//
// Only the one-line index enters the system prompt; the full skill
// body loads on demand when a keyword matches (the stage 17
// machinery). This is how the small model "learns from the big
// ones": expertise is written down once instead of re-derived per
// session. On-disk files win over the build-time embedded copies, so
// the library is editable without a rebuild.

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/FiveTechSoft/FiveAgent/internal/tools"
	skillsembed "github.com/FiveTechSoft/FiveAgent/skills"
)

// DefaultSkillsDir is the runtime location of the skill library,
// relative to the working directory.
const DefaultSkillsDir = "skills"

// ParseSkillMD parses one SKILL.md into a Skill. Name, trigger and a
// non-empty body are required; keywords default to the skill name.
func ParseSkillMD(text string) (Skill, error) {
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i < len(lines) && strings.HasPrefix(lines[i], "# ") {
		i++ // cosmetic title
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	var name, trigger string
	var keywords, toolNames []string
	for ; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			i++
			break
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			return Skill{}, fmt.Errorf("malformed header line %q (want key: value)", l)
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "name":
			name = v
		case "trigger":
			trigger = v
		case "keywords":
			keywords = splitCSV(v)
		case "tools":
			toolNames = splitCSV(v)
		default:
			return Skill{}, fmt.Errorf("unknown header key %q", k)
		}
	}
	body := strings.TrimSpace(strings.Join(lines[i:], "\n"))
	if name == "" {
		return Skill{}, errors.New("missing name")
	}
	if trigger == "" {
		return Skill{}, errors.New("missing trigger line")
	}
	if body == "" {
		return Skill{}, errors.New("empty body")
	}
	if len(keywords) == 0 {
		keywords = []string{name}
	}
	return Skill{
		Name:        name,
		TriggerLine: trigger,
		Triggers:    keywords,
		Tools:       toolNames,
		Load:        func() string { return body },
	}, nil
}

func splitCSV(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// LoadSkillsDir reads every <dir>/<name>/SKILL.md. On-disk files win;
// a skill missing on disk falls back to the copy embedded at build
// time. One malformed file is skipped with a log line - it never
// takes the library down.
func LoadSkillsDir(dir string) []Skill {
	var out []Skill
	seen := map[string]bool{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			p := filepath.Join(dir, e.Name(), "SKILL.md")
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			sk, err := ParseSkillMD(string(b))
			if err != nil {
				log.Printf("skills: %s skipped: %v", p, err)
				continue
			}
			if !seen[sk.Name] {
				seen[sk.Name] = true
				out = append(out, sk)
			}
		}
	}
	if entries, err := skillsembed.FS.ReadDir("."); err == nil {
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			b, err := skillsembed.FS.ReadFile(e.Name() + "/SKILL.md")
			if err != nil {
				continue
			}
			sk, err := ParseSkillMD(string(b))
			if err != nil || seen[sk.Name] {
				continue
			}
			seen[sk.Name] = true
			out = append(out, sk)
		}
	}
	return out
}

// SkillsIndex renders the one-line index that rides in the system
// prompt on every turn: names and trigger lines only, so the prompt
// pays one line per skill, never the full text.
func SkillsIndex(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nSkills (one line each; the full text enters the context automatically when a trigger word matches):")
	for _, sk := range skills {
		fmt.Fprintf(&b, "\n- %s: %s", sk.Name, sk.TriggerLine)
	}
	return b.String()
}

// toolSpecsFor returns the registry specs offered to the model on this
// turn. A tool named by a skill's "tools:" header rides only when that
// skill triggered; tools named by no skill are always offered.
func (a *Agent) toolSpecsFor(triggered map[string]bool) []tools.Spec {
	specs := a.tools.Specs()
	gated := map[string]string{} // tool name -> owning skill
	for _, sk := range a.skills {
		for _, tn := range sk.Tools {
			gated[tn] = sk.Name
		}
	}
	if len(gated) == 0 {
		return specs
	}
	out := make([]tools.Spec, 0, len(specs))
	for _, sp := range specs {
		if owner, ok := gated[sp.Function.Name]; ok && !triggered[owner] {
			continue
		}
		out = append(out, sp)
	}
	return out
}

// DomainSkill returns the FiveTech domain skill from the library
// (skills/fivetech/SKILL.md), for callers that wire skills one by one.
// New code should prefer LoadSkillsDir(DefaultSkillsDir) so every
// skill rides along.
func DomainSkill() Skill {
	for _, sk := range LoadSkillsDir(DefaultSkillsDir) {
		if sk.Name == "fivetech-domain" {
			return sk
		}
	}
	return Skill{Name: "fivetech-domain", Load: func() string { return "" }}
}
