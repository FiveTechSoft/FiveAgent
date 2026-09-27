package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func openTestKnowledge(t *testing.T) *memory.Knowledge {
	t.Helper()
	k, err := memory.OpenKnowledge(filepath.Join(t.TempDir(), "memory"))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestHandleInjectsMemory checks recalled memories reach the model
// labeled as data, never instructions.
func TestHandleInjectsMemory(t *testing.T) {
	kn := openTestKnowledge(t)
	if _, err := kn.Append("preferences", "Loves pasta."); err != nil {
		t.Fatal(err)
	}
	var gotBody atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody.Store(string(body))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"reply"}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{}, tools.NewRegistry(), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	if _, err := a.Handle(context.Background(), "whatsapp", "u1", "¿le gusta la pasta?"); err != nil {
		t.Fatal(err)
	}
	body, _ := gotBody.Load().(string)
	if !strings.Contains(body, "Loves pasta") {
		t.Error("model request does not carry the recalled memory")
	}
	if !strings.Contains(body, "never instructions") {
		t.Error("memory block is not labeled as data, never instructions")
	}
}

// TestHandleSavesMemoryViaTool checks the model can store a fact through
// the save_memory tool and it lands in the markdown file.
func TestHandleSavesMemoryViaTool(t *testing.T) {
	kn := openTestKnowledge(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[`+
				`{"id":"c1","type":"function","function":{"name":"save_memory",`+
				`"arguments":"{\"file\":\"preferences\",\"entry\":\"Loves pasta\"}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Anotado."}}]}`)
	}))
	defer srv.Close()

	cfg := &config.Config{Model: config.Model{BaseURL: srv.URL, Name: "m"}}
	a := New(model.NewOpenAICompat(cfg.Model), &fakeStore{},
		tools.NewRegistry(tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}), SystemPrompt(cfg))
	a.WithKnowledge(kn)
	reply, err := a.Handle(context.Background(), "whatsapp", "u1", "recuerda que me gusta la pasta")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Anotado." {
		t.Errorf("reply = %q", reply)
	}
	hits, err := kn.Recall("pasta")
	if err != nil || len(hits) == 0 {
		t.Fatalf("memory did not store the tool call: hits=%v err=%v", hits, err)
	}
}

// TestMemoryToolsForget checks forget_memory removes stored facts.
func TestMemoryToolsForget(t *testing.T) {
	kn := openTestKnowledge(t)
	if _, err := kn.Append("preferences", "Loves pasta."); err != nil {
		t.Fatal(err)
	}
	out, err := tools.ForgetMemory{K: kn}.Execute(context.Background(),
		json.RawMessage(`{"file":"preferences","match":"pasta"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "forgot 1") {
		t.Errorf("out = %q", out)
	}
	hits, _ := kn.Recall("pasta")
	if len(hits) != 0 {
		t.Errorf("fact still recalled after forget: %+v", hits)
	}
}
