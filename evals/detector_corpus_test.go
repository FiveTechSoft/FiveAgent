package evals

// Reserved, versioned corpus for the hallucination/abstention detector.
// Every case carries a fixed gold label (see detector_corpus/README.md).
// The original detector (frozen at 6bc4863, detector_original_test.go) and
// the current one are run over the same cases and compared against the
// gold labels. final.jsonl is sealed by hash: editing it fails the test,
// so it cannot be tuned against silently. No model is involved.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type corpusCase struct {
	ID                string   `json:"id"`
	Kind              string   `json:"kind"` // good | seeded | near
	Category          string   `json:"category"`
	Prompt            string   `json:"prompt"`
	Reply             string   `json:"reply"`
	MustNotContain    []string `json:"must_not_contain"`
	Audit             string   `json:"audit"`
	GoldHallucination bool     `json:"gold_hallucination"`
	GoldAbstention    bool     `json:"gold_abstention"`
	Note              string   `json:"note"`
}

const corpusDir = "detector_corpus"

func loadCorpus(t *testing.T, name string) []corpusCase {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpusDir, name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []corpusCase
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var c corpusCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, c)
	}
	return out
}

// verdict mirrors the order of the live battery switch: a must-not token
// is checked first, an abstention never counts as an error narration, and
// the audit comparison runs only for a non-abstention.
type verdict struct {
	Hallucination bool `json:"hallucination"`
	Abstention    bool `json:"abstention"`
}

func currentVerdict(c corpusCase) verdict {
	low := strings.ToLower(c.Reply)
	ab := abstains(c.Reply)
	flag := hallucinationToken(low, strings.ToLower(c.Prompt), c.MustNotContain, ab) != ""
	if !flag && !ab && c.Audit != "" {
		flag = inventedErrorNarration(low, c.Audit) != ""
	}
	return verdict{flag, ab}
}

func originalVerdict(c corpusCase) verdict {
	low := strings.ToLower(c.Reply)
	ab := origAbstains(c.Reply)
	flag := origHallucinationToken(low, strings.ToLower(c.Prompt), c.MustNotContain, ab) != ""
	if !flag && !ab && c.Audit != "" {
		flag = origInventedErrorNarration(low, c.Audit) != ""
	}
	return verdict{flag, ab}
}

type confusion struct{ TP, FP, FN, TN int }

func (c *confusion) add(gold, got bool) {
	switch {
	case gold && got:
		c.TP++
	case !gold && got:
		c.FP++
	case gold && !got:
		c.FN++
	default:
		c.TN++
	}
}

func (c confusion) String() string {
	prec, rec := "n/a", "n/a"
	if c.TP+c.FP > 0 {
		prec = fmt.Sprintf("%.2f", float64(c.TP)/float64(c.TP+c.FP))
	}
	if c.TP+c.FN > 0 {
		rec = fmt.Sprintf("%.2f", float64(c.TP)/float64(c.TP+c.FN))
	}
	return fmt.Sprintf("TP=%d FP=%d FN=%d TN=%d precision=%s recall=%s", c.TP, c.FP, c.FN, c.TN, prec, rec)
}

type corpusResult struct {
	ID       string  `json:"id"`
	Original verdict `json:"original"`
	Current  verdict `json:"current"`
}

func runCorpus(t *testing.T, name string, cases []corpusCase) []corpusResult {
	var res []corpusResult
	var hO, hC, aO, aC confusion
	agree := 0
	for _, c := range cases {
		o, n := originalVerdict(c), currentVerdict(c)
		res = append(res, corpusResult{c.ID, o, n})
		hO.add(c.GoldHallucination, o.Hallucination)
		hC.add(c.GoldHallucination, n.Hallucination)
		aO.add(c.GoldAbstention, o.Abstention)
		aC.add(c.GoldAbstention, n.Abstention)
		if o == n {
			agree++
		}
		if o != n || n.Hallucination != c.GoldHallucination || n.Abstention != c.GoldAbstention {
			t.Logf("%s %s [%s] gold(h=%v a=%v) orig(h=%v a=%v) current(h=%v a=%v) %s",
				name, c.ID, c.Kind, c.GoldHallucination, c.GoldAbstention,
				o.Hallucination, o.Abstention, n.Hallucination, n.Abstention, c.Note)
		}
	}
	t.Logf("METRIC detector-corpus %s (%d cases) hallucination original: %s", name, len(cases), hO)
	t.Logf("METRIC detector-corpus %s hallucination current:  %s", name, hC)
	t.Logf("METRIC detector-corpus %s abstention original: %s", name, aO)
	t.Logf("METRIC detector-corpus %s abstention current:  %s", name, aC)
	t.Logf("METRIC detector-corpus %s original and current agree on %d/%d cases", name, agree, len(cases))
	return res
}

func TestDetectorCorpusIsSealedAndWellFormed(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(corpusDir, "final.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	want, err := os.ReadFile(filepath.Join(corpusDir, "FINAL.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sum[:]); !strings.HasPrefix(string(want), got) {
		t.Fatalf("final.jsonl changed (sha256 %s): the final set is sealed; adding a new sealed set is a deliberate, reviewed act", got)
	}
	seen := map[string]bool{}
	for _, name := range []string{"dev", "final"} {
		for _, c := range loadCorpus(t, name) {
			if seen[c.ID] {
				t.Errorf("duplicate id %s", c.ID)
			}
			seen[c.ID] = true
			if c.Reply == "" || c.Prompt == "" || (c.Kind != "good" && c.Kind != "seeded" && c.Kind != "near") {
				t.Errorf("%s malformed", c.ID)
			}
			if c.Kind == "seeded" && !c.GoldHallucination {
				t.Errorf("%s: a seeded failure must carry a hallucination label", c.ID)
			}
			if c.Kind == "good" && c.GoldHallucination {
				t.Errorf("%s: a good answer cannot be a hallucination", c.ID)
			}
		}
	}
}

// TestDetectorCorpusSnapshot pins the per-case verdicts of both
// detectors. A change in either detector flips verdicts and fails here,
// so a detector edit always shows up as a visible diff of results.json
// (regenerate with UPDATE_DETECTOR_RESULTS=1) next to the final-set
// numbers it produces.
func TestDetectorCorpusSnapshot(t *testing.T) {
	all := map[string][]corpusResult{}
	for _, name := range []string{"dev", "final"} {
		all[name] = runCorpus(t, name, loadCorpus(t, name))
	}
	path := filepath.Join(corpusDir, "results.json")
	enc, _ := json.MarshalIndent(all, "", " ")
	if os.Getenv("UPDATE_DETECTOR_RESULTS") == "1" {
		if err := os.WriteFile(path, append(enc, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var old map[string][]corpusResult
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	var diffs []string
	for name, rows := range all {
		prev := map[string]corpusResult{}
		for _, r := range old[name] {
			prev[r.ID] = r
		}
		for _, r := range rows {
			if p, ok := prev[r.ID]; !ok || p != r {
				diffs = append(diffs, r.ID)
			}
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Fatalf("detector verdicts changed on %v: review, then regenerate results.json", diffs)
	}
}
