// Package evals measures the memory system with scripted conversations.
//
// Two tiers:
//
//   - Deterministic system evals (run in CI): a scripted fake model plays
//     both sides of the conversation, so the numbers say how the memory
//     SYSTEM behaves: what gets injected, whether recall survives
//     truncation, whether corrections stick, and what each turn costs.
//   - Live model evals (FIVEAGENT_EVAL_LIVE=1, run locally): a real model
//     decides what to save and how to use memories. Non-deterministic and
//     needs a running model server, so it is a manual quality gate, not
//     CI.
package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

// request is the part of the chat request the evals inspect.
type request struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// player is a scripted fake model. It answers according to the last user
// message and how many tool results the turn already carries:
//
//	"remember: <entry>"   -> save_memory(preferences, <entry>), then "Anotado."
//	"correct: <old> -> <new>" -> forget_memory(preferences, <old>),
//		save_memory(preferences, <new>), then "Corregido."
//	anything else         -> "respuesta"
//
// The last request body is captured for inspection.
type player struct {
	lastRequest atomic_store
}

type atomic_store struct {
	ch chan string
}

func newPlayer() *player { return &player{lastRequest: atomic_store{ch: make(chan string, 1024)}} }

func (p *player) last() string {
	var last string
	for {
		select {
		case s := <-p.lastRequest.ch:
			last = s
		default:
			return last
		}
	}
}

// drainAll returns every request the player has seen, oldest first.
func (p *player) drainAll() []string {
	var out []string
	for {
		select {
		case s := <-p.lastRequest.ch:
			out = append(out, s)
		default:
			return out
		}
	}
}

func reply(content string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%q}}]}`, content)
}

func toolCall(name, args string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","tool_calls":[`+
		`{"id":"c1","type":"function","function":{"name":%q,"arguments":%q}}]}}]}`, name, args)
}

func (p *player) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	p.lastRequest.ch <- string(body)
	var req request
	_ = json.Unmarshal(body, &req)
	var lastUser string
	toolResults := 0
	for _, m := range req.Messages {
		if m.Role == "user" {
			lastUser = m.Content
		}
		if m.Role == "tool" {
			toolResults++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	// Stage 7n: the auto-indexer extraction call is recognizable by
	// its system prompt; the player answers it as a real extractor
	// would - the fact line when the exchange carries the fact, NONE
	// when it does not. Answering every call with the fact would
	// index it into scopes and turns where it never happened,
	// silently faking the isolation the battery claims to prove.
	extraction := false
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "extract durable facts") {
			extraction = true
		}
	}
	// Stage 34: a subturn (stage 18 machinery) is recognizable by
	// its system prompt; the player sleeps so the parallel battery
	// proves real overlap from wall time, not from construction.
	subturn := false
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "solving ONE small subtask") {
			subturn = true
		}
	}
	switch {
	case subturn:
		time.Sleep(250 * time.Millisecond)
		io.WriteString(w, reply("hecho"))
	case extraction && strings.Contains(string(body), "Celta"):
		io.WriteString(w, reply("preferences: su equipo favorito es el Celta de Vigo desde siempre"))
	case extraction:
		io.WriteString(w, reply("NONE"))
	case strings.HasPrefix(lastUser, "remember: "):
		entry := strings.TrimPrefix(lastUser, "remember: ")
		if toolResults == 0 {
			io.WriteString(w, toolCall("save_memory",
				fmt.Sprintf(`{"file":"preferences","entry":%q}`, entry)))
			return
		}
		io.WriteString(w, reply("Anotado."))
	case strings.HasPrefix(lastUser, "forget: "):
		entry := strings.TrimPrefix(lastUser, "forget: ")
		if toolResults == 0 {
			io.WriteString(w, toolCall("forget_memory",
				fmt.Sprintf(`{"file":"preferences","match":%q}`, entry)))
			return
		}
		io.WriteString(w, reply("Olvidado."))
	case strings.HasPrefix(lastUser, "correct: "):
		rest := strings.TrimPrefix(lastUser, "correct: ")
		parts := strings.SplitN(rest, " -> ", 2)
		if len(parts) != 2 {
			io.WriteString(w, reply("formato raro"))
			return
		}
		switch toolResults {
		case 0:
			io.WriteString(w, toolCall("forget_memory",
				fmt.Sprintf(`{"file":"preferences","match":%q}`, parts[0])))
		case 1:
			io.WriteString(w, toolCall("save_memory",
				fmt.Sprintf(`{"file":"preferences","entry":%q}`, parts[1])))
		default:
			io.WriteString(w, reply("Corregido."))
		}
	case strings.HasPrefix(lastUser, "paralelo: "):
		if toolResults == 0 {
			io.WriteString(w, toolCall("run_subtasks",
				`{"tasks":["tarea alfa independiente","tarea beta independiente","tarea gamma independiente"]}`))
			return
		}
		io.WriteString(w, reply("listo"))
	default:
		io.WriteString(w, reply("respuesta"))
	}
}

// rig wires a full agent against the scripted player: real JSON history
// store, real memory folder, real tools.
type rig struct {
	agent  *agent.Agent
	kn     *memory.Knowledge
	player *player
	srv    *httptest.Server
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	kn, err := memory.OpenKnowledge(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := memory.OpenJSON(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := newPlayer()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "eval"}}
	a := agent.New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
		agent.SystemPrompt(cfg))
	a.WithKnowledge(kn)
	return &rig{agent: a, kn: kn, player: p, srv: srv}
}

func (r *rig) turn(t *testing.T, text string) string {
	t.Helper()
	return r.turnAs(t, "eval-user", text)
}

func (r *rig) turnAs(t *testing.T, userID, text string) string {
	t.Helper()
	reply, err := r.agent.Handle(context.Background(), "whatsapp", userID, text)
	if err != nil {
		t.Fatalf("turn %q: %v", text, err)
	}
	return reply
}

// injectedMemory returns the memory recall block of the captured request,
// or "" when nothing was injected.
func injectedMemory(t *testing.T, body string) string {
	t.Helper()
	var req request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	for _, m := range req.Messages {
		if m.Role == "system" && strings.Contains(m.Content, "Long-term memory recall") {
			return m.Content
		}
	}
	return ""
}

// historyTurns counts user+assistant messages in the captured request.
func historyTurns(t *testing.T, body string) int {
	t.Helper()
	var req request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range req.Messages {
		if m.Role == "user" || m.Role == "assistant" {
			n++
		}
	}
	return n
}

// TestRecallSurvivesTruncation: a fact stored in a middle turn must be
// injected on a later turn, when the turn that stored it has left the
// context. Metric: recall hit rate past context loss.
//
// Stage 15 replaced the hard 20-message truncation with budgeted
// pruning, and the pruning protects the head exchange - so a fact on
// turn 1 would survive verbatim and the test would prove nothing. The
// fact therefore lives at turn 12 with 1KB filler turns around it: 40
// turns later the middle (fact included) has been compacted away, and
// only memory can still answer.
func TestRecallSurvivesTruncation(t *testing.T) {
	r := newRig(t)
	r.turn(t, "hola")
	for i := 0; i < 10; i++ {
		r.turn(t, fmt.Sprintf("cuéntame un chiste número %d %s", i, strings.Repeat("ja ", 300)))
	}
	r.turn(t, "remember: my cat is named Neko")
	for i := 10; i < 40; i++ {
		r.turn(t, fmt.Sprintf("cuéntame un chiste número %d %s", i, strings.Repeat("ja ", 300)))
	}
	r.turn(t, "what is my cat called")
	body := r.player.last()

	if strings.Contains(body, "remember: my cat is named Neko") {
		t.Fatal("the fact turn is still in history: pruning did not happen, the eval proves nothing")
	}
	if !strings.Contains(body, "earlier turns") {
		t.Fatal("no pruning marker in the request: pruning did not happen, the eval proves nothing")
	}
	mem := injectedMemory(t, body)
	if !strings.Contains(mem, "Neko") {
		t.Fatalf("recall MISS past compaction; injected block: %q", mem)
	}
	t.Logf("METRIC recall past compaction: 1/1 (injected %d chars)", len(mem))
}

// TestCorrectionSticks: correcting a fact must remove the old value and
// inject the new one. Metric: correction success rate.
func TestCorrectionSticks(t *testing.T) {
	r := newRig(t)
	r.turn(t, "remember: my cat is named Neko")
	r.turn(t, "correct: Neko -> my cat is named Michi")
	r.turn(t, "what is my cat called")
	mem := injectedMemory(t, r.player.last())
	if strings.Contains(mem, "Neko") {
		t.Errorf("old fact survives correction; block: %q", mem)
	}
	if !strings.Contains(mem, "Michi") {
		t.Errorf("new fact not injected; block: %q", mem)
	}
	t.Log("METRIC correction: 1/1")
}

// TestInjectionPrecision: a query about one topic must not drag
// unrelated memories into the context. Metric: relevant lines / total
// injected memory lines.
func TestInjectionPrecision(t *testing.T) {
	r := newRig(t)
	for _, f := range [][2]string{
		{"people", "María is his sister."},
		{"preferences", "Loves pasta."},
		{"workstreams", "Redoing the pasta blog."},
	} {
		if _, err := r.kn.Append(f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	r.turn(t, "who is María")
	mem := injectedMemory(t, r.player.last())
	if !strings.Contains(mem, "María") {
		t.Fatalf("recall MISS for María; block: %q", mem)
	}
	lines := 0
	irrelevant := 0
	for _, ln := range strings.Split(mem, "\n") {
		if !strings.HasPrefix(ln, "[") {
			continue
		}
		lines++
		if !strings.Contains(ln, "María") {
			irrelevant++
		}
	}
	if lines == 0 {
		t.Fatal("no memory lines injected")
	}
	precision := float64(lines-irrelevant) / float64(lines)
	t.Logf("METRIC injection precision: %.2f (%d relevant of %d lines)", precision, lines-irrelevant, lines)
	if irrelevant > 0 {
		t.Errorf("%d irrelevant lines injected: %q", irrelevant, mem)
	}
}

// TestDuplicateSaveRejected: the same fact saved twice through the agent
// path lands once. Metric: dedup rejection rate.
func TestDuplicateSaveRejected(t *testing.T) {
	r := newRig(t)
	r.turn(t, "remember: my cat is named Neko")
	r.turn(t, "remember: my cat is named Neko")
	hits, err := r.kn.Recall("Neko")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, h := range hits {
		for _, ln := range h.Lines {
			if strings.Contains(ln, "Neko") {
				total++
			}
		}
	}
	if total != 1 {
		t.Errorf("Neko stored %d times, want 1", total)
	}
	t.Log("METRIC dedup rejection: 1/1")
}

// TestInjectionCost: with a small realistic store, the injected memory
// block must stay cheap. Metric: chars and estimated tokens per turn.
func TestInjectionCost(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 10; i++ {
		if _, err := r.kn.Append("preferences", fmt.Sprintf("Likes dish number %d with extra detail.", i)); err != nil {
			t.Fatal(err)
		}
	}
	r.turn(t, "dish number 3")
	mem := injectedMemory(t, r.player.last())
	estTokens := len(mem) / 4
	t.Logf("METRIC injection cost: %d chars (~%d tokens) for a 10-note store", len(mem), estTokens)
	const maxChars = 4000 // guardrail: memory must not swallow the context
	if len(mem) > maxChars {
		t.Errorf("injected block %d chars exceeds the %d-char guardrail", len(mem), maxChars)
	}
}

// TestLiveModelMemory runs the full loop against a real model server
// (FIVEAGENT_EVAL_LIVE=1, optional FIVEAGENT_EVAL_BASE_URL /
// FIVEAGENT_EVAL_MODEL). The model itself decides to save, and its final
// answer must use the memory. This is the model-judgment gate: the CI
// evals cannot measure it, and a stage is only "done" when this passes
// against the target model.
func TestLiveModelMemory(t *testing.T) {
	if os.Getenv("FIVEAGENT_EVAL_LIVE") != "1" {
		t.Skip("live eval: set FIVEAGENT_EVAL_LIVE=1 (needs a running model server)")
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
	a := agent.New(model.NewOpenAICompat(cfg.Model), store,
		tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}),
		agent.SystemPrompt(cfg))
	a.WithKnowledge(kn)
	ctx := context.Background()
	if _, err := a.Handle(ctx, "whatsapp", "live", "Recuerda: mi gato se llama Neko."); err != nil {
		t.Fatal(err)
	}
	hits, _ := kn.Recall("Neko")
	if len(hits) == 0 {
		t.Fatalf("live: the model did not save the fact (judgment miss)")
	}
	for i := 0; i < 25; i++ {
		if _, err := a.Handle(ctx, "whatsapp", "live", "cuéntame otra cosa"); err != nil {
			t.Fatal(err)
		}
	}
	reply, err := a.Handle(ctx, "whatsapp", "live", "¿cómo se llama mi gato?")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(reply), "neko") {
		t.Fatalf("live: answer %q does not use the memory", reply)
	}
	t.Log("METRIC live save+recall with real model: 1/1")
}

// TestRecallRate: with 10 distinct facts stored, every targeted query
// must inject its fact. Metric: recall hit rate over 10 queries.
func TestRecallRate(t *testing.T) {
	r := newRig(t)
	facts := []struct{ entry, term string }{
		{"My cat is named Neko.", "neko"},
		{"My dog is named Toby.", "toby"},
		{"I drink my coffee black.", "coffee"},
		{"My sister lives in Porto.", "porto"},
		{"I run on Tuesdays.", "tuesdays"},
		{"My bike is a red Orbea.", "orbea"},
		{"I hate coriander.", "coriander"},
		{"My Wi-Fi is called CasaLoma.", "casaloma"},
		{"I support Rayo Vallecano.", "rayo"},
		{"My editor is Neovim.", "neovim"},
	}
	for _, f := range facts {
		if _, err := r.kn.Append("preferences", f.entry); err != nil {
			t.Fatal(err)
		}
	}
	hits := 0
	for _, f := range facts {
		r.turn(t, "tell me about "+f.term)
		mem := injectedMemory(t, r.player.last())
		if strings.Contains(strings.ToLower(mem), f.term) {
			hits++
		} else {
			t.Errorf("recall MISS for %q; injected block: %q", f.term, mem)
		}
	}
	t.Logf("METRIC recall rate: %d/%d", hits, len(facts))
}

// TestAliasRecall: a query using a file alias ("contactos") must inject
// the people file even when no body word matches. Metric: alias hit
// rate.
func TestAliasRecall(t *testing.T) {
	r := newRig(t)
	if _, err := r.kn.Append("people", "María is his sister."); err != nil {
		t.Fatal(err)
	}
	r.turn(t, "mira mis contactos")
	mem := injectedMemory(t, r.player.last())
	if !strings.Contains(mem, "María") {
		t.Fatalf("alias recall MISS for contactos; injected block: %q", mem)
	}
	t.Log("METRIC alias recall: 1/1")
}

// TestInjectionCostScaling: the injected block must stay bounded as the
// store grows, even when many notes match the query. Metric: chars and
// estimated tokens per turn with a 50-note store.
func TestInjectionCostScaling(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 50; i++ {
		if _, err := r.kn.Append("preferences", fmt.Sprintf("Likes dish number %d with extra detail.", i)); err != nil {
			t.Fatal(err)
		}
	}
	r.turn(t, "dish")
	mem := injectedMemory(t, r.player.last())
	estTokens := len(mem) / 4
	t.Logf("METRIC injection cost at 50 notes: %d chars (~%d tokens)", len(mem), estTokens)
	const maxChars = 4000
	if len(mem) > maxChars {
		t.Errorf("injected block %d chars exceeds the %d-char guardrail", len(mem), maxChars)
	}
	if !strings.Contains(mem, "dish number 49") {
		t.Errorf("recall cap must keep the most recent notes; block: %q", mem)
	}
}

// TestForgetByAgent: the model can delete a fact through forget_memory
// and it stops being injected. Metric: forget success rate.
func TestForgetByAgent(t *testing.T) {
	r := newRig(t)
	r.turn(t, "remember: my cat is named Neko")
	r.turn(t, "forget: Neko")
	r.turn(t, "what is my cat called")
	mem := injectedMemory(t, r.player.last())
	if strings.Contains(mem, "Neko") {
		t.Fatalf("forgotten fact still injected; block: %q", mem)
	}
	t.Log("METRIC forget by agent: 1/1")
}

// TestHistoryIsolation: one sender's conversation must never leak into
// another sender's request. Metric: cross-sender history leaks (want 0).
func TestHistoryIsolation(t *testing.T) {
	r := newRig(t)
	r.turnAs(t, "user-a", "mi palabra secreta es bananaphone")
	r.turnAs(t, "user-b", "hola, ¿qué sabes de mí?")
	body := r.player.last()
	if strings.Contains(body, "bananaphone") {
		t.Fatal("sender A history leaked into sender B request")
	}
	t.Log("METRIC cross-sender history leaks: 0/1")
}

// TestConcurrentSaves: parallel turns saving different facts must not
// race the git-backed store: every save either lands or is cleanly
// reported, and the store stays readable afterwards. Metric: facts
// stored / facts saved OK.
func TestConcurrentSaves(t *testing.T) {
	r := newRig(t)
	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			_, err := r.kn.Append("preferences", fmt.Sprintf("Concurrent fact number %d.", i))
			errs <- err
		}(i)
	}
	stored := 0
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Logf("concurrent append error: %v", err)
		}
	}
	for i := 0; i < n; i++ {
		hits, err := r.kn.Recall(fmt.Sprintf("number %d", i))
		if err != nil {
			t.Fatalf("store unreadable after concurrent saves: %v", err)
		}
		for _, h := range hits {
			for _, ln := range h.Lines {
				if strings.Contains(ln, fmt.Sprintf("number %d", i)) {
					stored++
				}
			}
		}
	}
	t.Logf("METRIC concurrent saves: %d/%d facts stored and recallable", stored, n)
	if stored != n {
		t.Errorf("lost facts under concurrency: %d of %d stored", stored, n)
	}
}
