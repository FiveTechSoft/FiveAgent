// Command fiveagent runs the FiveAgent personal agent as a single binary.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/channel"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/sched"
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func main() {
	// fiveagent doctor: probe this machine's sandbox capabilities and
	// exit (no config, no model, no channels needed).
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		cfg, err := config.Load("")
		if err != nil {
			cfg = &config.Config{} // doctor works without a yml
		}
		for _, l := range sandbox.DoctorReport(cfg.Sandbox) {
			fmt.Println(l)
		}
		return
	}

	cfg, err := config.Load(os.Getenv("FIVEAGENT_CONFIG"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mdl := model.NewOpenAICompat(cfg.Model)
	var store memory.Store
	if cfg.Memory.JSON != "" {
		store, err = memory.OpenJSON(cfg.Memory.JSON)
	} else {
		store, err = memory.OpenPostgres(cfg.Memory.Postgres)
	}
	if err != nil {
		log.Fatalf("memory: %v", err)
	}
	defer store.Close()

	tl := []tools.Tool{tools.Datetime{}}
	// Stage 22: the scheduler is created before the registry so the
	// model gets schedule_job; its deliver closure resolves channels
	// lazily, once they are built below, and Run starts once ctx
	// exists.
	var chans []channel.Channel
	var sc *sched.Scheduler
	if cfg.Cron.Enabled {
		cronPath := cfg.Cron.Path
		if cronPath == "" {
			cronPath = "data/jobs.json"
		}
		deliver := func(ctx context.Context, channelName, userID, text string) error {
			for _, ch := range chans {
				if ch.Name() != channelName {
					continue
				}
				d, ok := ch.(interface {
					Deliver(context.Context, string, string) error
				})
				if !ok {
					return fmt.Errorf("cron: channel %s cannot deliver scheduled jobs", channelName)
				}
				return d.Deliver(ctx, userID, text)
			}
			return fmt.Errorf("cron: channel %s is not enabled", channelName)
		}
		var err error
		sc, err = sched.Open(cronPath, deliver)
		if err != nil {
			log.Printf("cron disabled: %v", err)
			sc = nil
		} else {
			tl = append(tl, tools.ScheduleJob{Sched: sc})
			log.Printf("cron scheduler: %s", cronPath)
		}
	}
	if cfg.Sandbox.Enabled {
		if sb, err := sandbox.New(cfg.Sandbox); err != nil {
			log.Printf("sandbox disabled: %v", err)
		} else {
			tl = append(tl, tools.RunCommand{SB: sb})
			log.Printf("sandbox: %s backend", sb.Name())
		}
	}
	if cfg.Workspace.Enabled {
		root := cfg.Workspace.Root
		if root == "" {
			root = "data/workspace"
		}
		ws := tools.Workspace{Root: root}
		tl = append(tl, tools.ReadFile{WS: ws}, tools.WriteFile{WS: ws}, tools.EditFile{WS: ws})
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
			tl = append(tl, tools.SaveMemory{K: kn}, tools.ForgetMemory{K: kn})
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
	reg := tools.NewRegistry(tl...)
	core := agent.New(mdl, store, reg, agent.SystemPrompt(cfg))
	core.WithSkills(agent.DomainSkill())
	if kn != nil {
		core.WithKnowledge(kn)
	}
	if cfg.Coder.Name != "" {
		core.WithCoder(model.NewOpenAICompat(cfg.Coder))
		log.Printf("coder model: %s (auto-routed for code requests)", cfg.Coder.Name)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chans = channel.Build(cfg, core)
	if sc != nil {
		go sc.Run(ctx)
	}
	for _, ch := range chans {
		go func(ch channel.Channel) {
			if err := ch.Run(ctx); err != nil {
				log.Printf("channel %s: %v", ch.Name(), err)
			}
		}(ch)
		log.Printf("channel %s started", ch.Name())
	}

	<-ctx.Done()
	log.Println("shutting down")
}
