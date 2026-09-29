package evals

// trajectory_test.go - stage 12 done-when battery case: a scripted
// turn produces a trajectory that is valid for the dataset schema -
// messages with roles and tool calls, the outcome, and per-tool
// stats - with sensitive shapes redacted before any sink sees them.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
	"github.com/FiveTechSoft/FiveAgent/internal/trajectory"
)

func TestScriptedTurnWritesValidTrajectory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(string(b), `"role":"tool"`) {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"son las 12:30"}}]}`)
		} else {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"current_datetime","arguments":"{}"}}]}}]}`)
		}
	}))
	t.Cleanup(srv.Close)

	store, err := memory.OpenJSON(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Model{BaseURL: srv.URL, Name: "eval", Timeout: 10}
	a := agent.New(model.NewOpenAICompat(cfg), store, tools.NewRegistry(tools.Datetime{}), agent.SystemPrompt(&config.Config{}))

	dir := t.TempDir()
	logger, err := trajectory.Open(dir, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.WithTrajectory(func(r trajectory.Record) {
		if err := logger.LogCase("trajectory/0", r); err != nil {
			t.Errorf("LogCase: %v", err)
		}
	})

	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "dime la hora, mi tfno es +34 600 123 456")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "son las 12:30" {
		t.Fatalf("the turn must complete normally, got %q", reply)
	}

	data, err := os.ReadFile(filepath.Join(dir, "trajectory_0.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "600 123 456") {
		t.Fatal("the trajectory must be redacted before it reaches disk")
	}
	var rec trajectory.Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &rec); err != nil {
		t.Fatalf("the case file must be one valid JSONL trajectory: %v", err)
	}

	// Schema: messages with roles and tool calls, outcome, tool stats.
	if len(rec.Messages) != 3 {
		t.Fatalf("user + assistant(tool_calls) + tool = 3 messages, got %d: %+v", len(rec.Messages), rec.Messages)
	}
	if rec.Messages[0].Role != "user" || rec.Messages[1].Role != "assistant" || rec.Messages[2].Role != "tool" {
		t.Fatalf("message roles out of order: %+v", rec.Messages)
	}
	if len(rec.Messages[1].ToolCalls) != 1 || rec.Messages[1].ToolCalls[0].Name != "current_datetime" {
		t.Fatalf("the assistant tool call must be recorded: %+v", rec.Messages[1])
	}
	if rec.Messages[2].Name != "current_datetime" || rec.Messages[2].Content == "" {
		t.Fatalf("the tool result must be recorded with its tool name: %+v", rec.Messages[2])
	}
	if rec.Outcome.Reply != "son las 12:30" || rec.Outcome.ToolRounds != 1 {
		t.Errorf("outcome wrong: %+v", rec.Outcome)
	}
	st := rec.ToolStats["current_datetime"]
	if st == nil || st.Calls != 1 || st.OK != 1 || st.Fail != 0 {
		t.Fatalf("tool stats wrong: %+v", rec.ToolStats)
	}
	// The record carries the channel, never the user's identity.
	if rec.Channel != "whatsapp" || strings.Contains(string(data), "u1") {
		t.Errorf("channel recorded, user identity absent: %q / %s", rec.Channel, data)
	}
}
