// parallel.go - parallel subtasks (stage 34 of docs/ROADMAP.md),
// on the stage 18 subturn machinery.
//
// run_subtask runs ONE subtask; run_subtasks runs SEVERAL
// independent subtasks at once through a bounded worker pool
// (goroutines fed by a task queue). Each subtask is the same
// isolated subturn as stage 18 - fresh context, no history, no
// memory, every tool except the delegation tools - so workers share
// nothing mutable, and delegation stays capped at depth 1. A
// failing subtask fills its own slot with an honest error; the
// others still run. The main turn is the coordinator: it gets every
// result in order and synthesizes the final answer.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const (
	// maxParallelSubtasks bounds the worker pool: a small model
	// endpoint behind the agent is usually the bottleneck, and an
	// unbounded fan-out would also multiply cost per turn.
	maxParallelSubtasks = 4
	// maxSubtasksPerCall bounds one fan-out: more steps than this
	// belong in several run_subtasks calls, not one giant turn.
	maxSubtasksPerCall = 8
)

// subResult is one finished slot of a parallel fan-out.
type subResult struct {
	task  string
	reply string
	err   error
}

// runSubTurns runs tasks through a bounded worker pool and returns
// one result per task, in order. Worker i writes only results[i], so
// the pool is race-free by construction (go test -race covers it).
func (a *Agent) runSubTurns(ctx context.Context, tasks []string) []subResult {
	results := make([]subResult, len(tasks))
	workers := maxParallelSubtasks
	if len(tasks) < workers {
		workers = len(tasks)
	}
	jobs := make(chan int, len(tasks))
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				reply, err := a.runSubTurn(ctx, tasks[i])
				if err != nil {
					err = fmt.Errorf("subtask %d: %w", i+1, err)
				}
				results[i] = subResult{task: tasks[i], reply: reply, err: err}
			}
		}()
	}
	for i := range tasks {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

// runSubtasksTool is the parallel sibling of run_subtask.
type runSubtasksTool struct {
	a *Agent
}

// Name implements tools.Tool.
func (runSubtasksTool) Name() string { return "run_subtasks" }

// Description implements tools.Tool.
func (runSubtasksTool) Description() string {
	return "Run SEVERAL independent subtasks in parallel, each in its own isolated helper turn with a fresh context (no conversation history), and get every result in order. Faster than repeated run_subtask calls when the steps do not depend on each other. One failing subtask does not stop the others: its slot comes back with an honest error. Compose the final answer from all the results."
}

// Parameters implements tools.Tool.
func (runSubtasksTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"tasks": {
				"type": "array",
				"items": {"type": "string"},
				"description": "The subtasks to run in parallel (1-8). Each must be complete and self-contained, and independent of the others."
			}
		},
		"required": ["tasks"]
	}`)
}

// Execute implements tools.Tool.
func (t runSubtasksTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Tasks []string `json:"tasks"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("run_subtasks: bad arguments: %w", err)
	}
	if len(a.Tasks) == 0 {
		return "", fmt.Errorf("run_subtasks: empty task list")
	}
	if len(a.Tasks) > maxSubtasksPerCall {
		return "", fmt.Errorf("run_subtasks: %d tasks exceeds the %d-task cap - split the work into several calls", len(a.Tasks), maxSubtasksPerCall)
	}
	tasks := make([]string, len(a.Tasks))
	for i, task := range a.Tasks {
		tasks[i] = strings.TrimSpace(task)
		if tasks[i] == "" {
			return "", fmt.Errorf("run_subtasks: task %d is empty", i+1)
		}
	}
	results := t.a.runSubTurns(ctx, tasks)
	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "=== Subtask %d: %s\n", i+1, r.task)
		if r.err != nil {
			fmt.Fprintf(&b, "error: %v\n", r.err)
		} else {
			b.WriteString(r.reply + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
