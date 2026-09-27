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
)

func main() {
	cfg, err := config.Load(os.Getenv("FIVEAGENT_CONFIG"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	mdl := model.NewOpenAICompat(cfg.Model)
	store, err := memory.Open(cfg.Memory.Postgres)
	if err != nil {
		log.Fatalf("memory: %v", err)
	}
	defer store.Close()

	core := agent.New(mdl, store)

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
