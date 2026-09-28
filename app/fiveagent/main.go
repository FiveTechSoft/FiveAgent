// Command fiveagent runs the FiveAgent personal agent as a single binary.
package main

import (
	"context"
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
	"github.com/FiveTechSoft/FiveAgent/internal/tools"
)

func main() {
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
	if cfg.Sandbox.Enabled {
		if sb, err := sandbox.New(cfg.Sandbox); err != nil {
			log.Printf("sandbox disabled: %v", err)
		} else {
			tl = append(tl, tools.RunCommand{SB: sb})
			log.Printf("sandbox: %s backend", sb.Name())
		}
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
	if kn != nil {
		core.WithKnowledge(kn)
	}
	if cfg.Coder.Name != "" {
		core.WithCoder(model.NewOpenAICompat(cfg.Coder))
		log.Printf("coder model: %s (auto-routed for code requests)", cfg.Coder.Name)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chans := channel.Build(cfg, core)
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
