package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func TestSkillScopeAtDispatch(t *testing.T) {
	for _, helper := range []int{0, 1, 2} {
		for _, active := range []bool{false, true} {
			t.Run(fmt.Sprintf("helper=%v/active=%v", helper, active), func(t *testing.T) {
				probe := &permissionProbe{name: "current_datetime"}
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var req struct {
						Messages []model.Message `json:"messages"`
						Tools    []tools.Spec    `json:"tools"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					last := req.Messages[len(req.Messages)-1]
					if last.Role == "tool" {
						b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": last.Content}}}})
						w.Write(b)
						return
					}
					// Even a model that names a hidden tool must not widen its scope.
					if helper > 0 && strings.HasPrefix(req.Messages[0].Content, "sys") {
						if helper == 2 {
							fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"helpers","type":"function","function":{"name":"run_subtasks","arguments":"{\"tasks\":[\"query one\",\"query two\"]}"}}]}}]}`)
							return
						}
						fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"helper","type":"function","function":{"name":"run_subtask","arguments":"{\"task\":\"query the time\"}"}}]}}]}`)
						return
					}
					found := false
					for _, sp := range req.Tools {
						if sp.Function.Name == probe.name {
							found = true
						}
					}
					if found != active {
						t.Errorf("advertised=%v, active=%v", found, active)
					}
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"query","type":"function","function":{"name":"current_datetime","arguments":"{}"}}]}}]}`)
				}))
				defer srv.Close()
				a := New(model.NewOpenAICompat(config.Model{BaseURL: srv.URL, Name: "mock"}), &fakeStore{}, tools.NewRegistry(probe), "sys").WithSkills(Skill{Name: "clock", Triggers: []string{"clockword"}, Tools: []string{probe.name}, Load: func() string { return "clock procedure" }})
				text := "plain query"
				if active {
					text = "clockword query"
				}
				out, err := a.Handle(context.Background(), "test", "user-a", text)
				if err != nil {
					t.Fatal(err)
				}
				want := int32(0)
				if active {
					want = 1
					if helper == 2 {
						want = 2
					}
				}
				if n := probe.calls.Load(); n != want {
					t.Fatalf("tool executed %d times, want %d", n, want)
				}
				if active && !strings.Contains(out, "test/user-a") {
					t.Fatalf("lost scope: %q", out)
				}
				if !active && !strings.Contains(out, "error: unknown tool") {
					t.Fatalf("missing rejection: %q", out)
				}
			})
		}
	}
}

func TestSharedSkillToolUsesAnyActiveOwner(t *testing.T) {
	a := New(nil, &fakeStore{}, tools.NewRegistry(tools.Datetime{}), "sys")
	a.skills = []Skill{{Name: "first", Tools: []string{"current_datetime"}}, {Name: "second", Tools: []string{"current_datetime"}}}
	for _, owners := range []map[string]bool{{"first": true}, {"second": true}} {
		found := false
		for _, sp := range a.toolSpecsFor(owners) {
			if sp.Function.Name == "current_datetime" {
				found = true
			}
		}
		if !found {
			t.Fatalf("active owning skill did not enable shared tool: %v", owners)
		}
	}
}
