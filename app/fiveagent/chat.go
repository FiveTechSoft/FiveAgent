// Chat subcommand: talk to the agent from the terminal, with no
// messaging channels and no HTTP listener. It loads the same config and
// builds the same agent core (model, memory, channel-independent tools)
// as the server, so what you try here is what the channels get.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
	"github.com/FiveTechSoft/FiveAgent/internal/trajectory"
)

// runChat is the entry point for "fiveagent chat".
func runChat(args []string) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	configPath := fs.String("config", os.Getenv("FIVEAGENT_CONFIG"), "path to fiveagent.yml (default: FIVEAGENT_CONFIG or ./fiveagent.yml)")
	user := fs.String("user", "local", "user id for memory scoping")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return runChatIO(*configPath, *user, os.Stdin, os.Stdout)
}

func runChatIO(configPath, user string, in io.Reader, out io.Writer) int {
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(out, "config:", err)
		return 1
	}
	core, closeStore, err := buildChatAgent(cfg)
	if err != nil {
		fmt.Fprintln(out, "chat:", err)
		return 1
	}
	defer closeStore()
	fmt.Fprintf(out, "FiveAgent chat (model %s). Type your message; /salir to quit.\n", cfg.Model.Name)
	if err := chatLoop(context.Background(), core, user, in, out); err != nil {
		fmt.Fprintln(out, "chat:", err)
		return 1
	}
	return 0
}

// buildChatAgent mirrors the server wiring in main() for the tools that
// do not depend on a messaging channel: datetime, sandbox, workspace
// files, browser, long-term memory, web search, trajectory logging and
// the optional coder model. Channel-bound tools (cron delivery,
// proactive subscriptions, identity links, oauth integrations, charts,
// links) stay server-only.
func buildChatAgent(cfg *config.Config) (*agent.Agent, func() error, error) {
	mdl := model.NewOpenAICompat(cfg.Model)
	var store memory.Store
	var err error
	if cfg.Memory.JSON != "" {
		store, err = memory.OpenJSON(cfg.Memory.JSON)
	} else {
		store, err = memory.OpenPostgres(cfg.Memory.Postgres)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("memory: %w", err)
	}
	dt := tools.Datetime{}
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			store.Close()
			return nil, nil, fmt.Errorf("config: timezone %q: %w", cfg.Timezone, err)
		}
		dt.Zone = loc
	}
	tl := []tools.Tool{dt}
	if cfg.Sandbox.Enabled {
		if sb, err := sandbox.New(cfg.Sandbox); err != nil {
			log.Printf("sandbox disabled: %v", err)
		} else {
			tl = append(tl, tools.RunCommand{SB: sb, MaxOutput: cfg.Sandbox.MaxOutput})
			log.Printf("sandbox: %s backend", sb.Name())
		}
	}
	if cfg.Workspace.Enabled {
		root := cfg.Workspace.Root
		if root == "" {
			root = "data/workspace"
		}
		ws := tools.Workspace{Root: root}
		tl = append(tl, tools.ReadFile{WS: ws}, tools.WriteFile{WS: ws}, tools.EditFile{WS: ws},
			tools.GitStatus{WS: ws}, tools.GitLog{WS: ws}, tools.GitCommit{WS: ws})
		log.Printf("workspace file tools: %s", root)
	}
	if cfg.Browser.Enabled {
		auditDir := cfg.Browser.AuditDir
		if auditDir == "" {
			auditDir = "data/browser-audit"
		}
		br := &tools.Browser{AuditDir: auditDir}
		tl = append(tl, tools.BrowsePage{B: br}, tools.BrowserAct{B: br})
		log.Printf("browser tool: audit in %s", auditDir)
	}
	var kn *memory.Knowledge
	if cfg.Memory.Knowledge != "" {
		var err error
		kn, err = memory.OpenKnowledge(cfg.Memory.Knowledge)
		if err != nil {
			log.Printf("long-term memory disabled: %v", err)
			kn = nil
		} else {
			tl = append(tl, tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn}, tools.SaveLearning{K: kn})
			log.Printf("long-term memory: %s", cfg.Memory.Knowledge)
		}
	}
	if cfg.WebSearch.Enabled {
		var p tools.SearchProvider
		switch cfg.WebSearch.Provider {
		case "", "duckduckgo":
			p = tools.DuckDuckGo{}
		case "brave":
			if cfg.WebSearch.APIKey == "" {
				log.Printf("web search disabled: brave provider needs web_search.api_key")
			} else {
				p = tools.Brave{APIKey: cfg.WebSearch.APIKey}
			}
		default:
			log.Printf("web search disabled: unknown provider %q (duckduckgo or brave)", cfg.WebSearch.Provider)
		}
		if p != nil {
			tl = append(tl, tools.WebSearch{P: p, MaxResults: cfg.WebSearch.MaxResults})
			log.Printf("web search: %s provider", p.Name())
		}
	}
	var trajLogger *trajectory.Logger
	if cfg.Trajectory.Enabled {
		dir := cfg.Trajectory.Dir
		if dir == "" {
			dir = "data/trajectories"
		}
		if tl12, err := trajectory.Open(dir, cfg.Trajectory.MaxMB, cfg.Trajectory.MaxFiles); err != nil {
			log.Printf("trajectory logging disabled: %v", err)
		} else {
			trajLogger = tl12
			log.Printf("trajectory logging: %s", dir)
		}
	}
	reg := tools.NewRegistry(tl...)
	core := agent.New(mdl, store, reg, agent.SystemPrompt(cfg))
	core.WithSkills(agent.LoadSkillsDir(agent.DefaultSkillsDir)...)
	core.WithEffectConfirmation()
	core.WithSampling(cfg.Sampling)
	if trajLogger != nil {
		core.WithTrajectory(func(r trajectory.Record) {
			if err := trajLogger.Log(r); err != nil {
				log.Printf("trajectory log: %v", err)
			}
		})
	}
	if kn != nil {
		core.WithKnowledge(kn)
		if cfg.Memory.AutoIndex {
			core.WithIndexer(agent.NewIndexer(mdl, cfg.Memory.Knowledge))
		}
	}
	if cfg.Coder.Name != "" {
		core.WithCoder(model.NewOpenAICompat(cfg.Coder))
	}
	return core, store.Close, nil
}

// chatLoop is the read-eval-print loop: one line in, one agent reply
// out. Extracted from runChatIO so tests can drive it with pipes.
func chatLoop(ctx context.Context, core *agent.Agent, user string, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Fprint(out, "\ntu> ")
		if !sc.Scan() {
			fmt.Fprintln(out)
			return sc.Err()
		}
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "":
			continue
		case "/salir", "/quit", "/exit":
			fmt.Fprintln(out, "hasta luego")
			return nil
		case "/ayuda", "/help":
			fmt.Fprintln(out, "commands: /salir quits; anything else goes to the agent")
			continue
		}
		reply, err := core.Handle(ctx, "cli", user, line)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			continue
		}
		fmt.Fprintf(out, "fiveagent> %s\n", reply)
	}
}
