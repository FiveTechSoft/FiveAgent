// Command fiveagent runs the FiveAgent personal agent as a single binary.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/FiveTechSoft/FiveAgent/internal/agent"
	"github.com/FiveTechSoft/FiveAgent/internal/channel"
	"github.com/FiveTechSoft/FiveAgent/internal/config"
	"github.com/FiveTechSoft/FiveAgent/internal/google"
	"github.com/FiveTechSoft/FiveAgent/internal/links"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/oauth"
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
	var linkSvc *links.Service
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
	var oauthHandler http.Handler
	{
		tokenPath := cfg.Integrations.Gmail.TokenPath
		if tokenPath == "" {
			tokenPath = cfg.Integrations.Calendar.TokenPath
		}
		if tokenPath == "" {
			tokenPath = cfg.Integrations.Drive.TokenPath
		}
		if tokenPath == "" {
			tokenPath = "data/tokens.json"
		}
		store := &oauth.TokenStore{Path: tokenPath}
		providers := map[string]oauth.Config{}
		redirects := map[string]string{}
		if cfg.Integrations.Gmail.Enabled {
			ocfg := oauth.Config{
				ClientID:     cfg.Integrations.Gmail.ClientID,
				ClientSecret: cfg.Integrations.Gmail.ClientSecret,
				AuthURL:      google.AuthURL,
				TokenURL:     google.TokenURL,
				Scopes:       []string{"https://www.googleapis.com/auth/gmail.modify"},
			}
			gmailFor := func(ctx context.Context) (*google.Gmail, error) {
				c, err := oauth.Client(ctx, ocfg, store, "gmail")
				if err != nil {
					return nil, err
				}
				return &google.Gmail{HTTP: c}, nil
			}
			tl = append(tl, tools.GmailSearch{Client: gmailFor}, tools.GmailSend{Client: gmailFor})
			providers["gmail"] = ocfg
			redirects["gmail"] = cfg.Integrations.Gmail.RedirectURL
			log.Printf("gmail integration: connect at /oauth/gmail/start")
		}
		if cfg.Integrations.Calendar.Enabled {
			ocfg := oauth.Config{
				ClientID:     cfg.Integrations.Calendar.ClientID,
				ClientSecret: cfg.Integrations.Calendar.ClientSecret,
				AuthURL:      google.AuthURL,
				TokenURL:     google.TokenURL,
				Scopes:       []string{"https://www.googleapis.com/auth/calendar.events"},
			}
			calFor := func(ctx context.Context) (*google.Calendar, error) {
				c, err := oauth.Client(ctx, ocfg, store, "calendar")
				if err != nil {
					return nil, err
				}
				return &google.Calendar{HTTP: c}, nil
			}
			tl = append(tl, tools.CalendarList{Client: calFor}, tools.CalendarCreate{Client: calFor})
			providers["calendar"] = ocfg
			redirects["calendar"] = cfg.Integrations.Calendar.RedirectURL
			log.Printf("calendar integration: connect at /oauth/calendar/start")
		}
		if cfg.Integrations.Drive.Enabled {
			ocfg := oauth.Config{
				ClientID:     cfg.Integrations.Drive.ClientID,
				ClientSecret: cfg.Integrations.Drive.ClientSecret,
				AuthURL:      google.AuthURL,
				TokenURL:     google.TokenURL,
				Scopes:       []string{"https://www.googleapis.com/auth/drive.file"},
			}
			driveFor := func(ctx context.Context) (*google.Drive, error) {
				c, err := oauth.Client(ctx, ocfg, store, "drive")
				if err != nil {
					return nil, err
				}
				return &google.Drive{HTTP: c}, nil
			}
			tl = append(tl, tools.DriveList{Client: driveFor}, tools.DriveDownload{Client: driveFor}, tools.DriveUpload{Client: driveFor})
			providers["drive"] = ocfg
			redirects["drive"] = cfg.Integrations.Drive.RedirectURL
			log.Printf("drive integration: connect at /oauth/drive/start")
		}
		if len(providers) > 0 {
			oauthHandler = oauth.NewHandler(providers, store,
				func(name string) string {
					return strings.TrimSuffix(redirects[name], "/") + "/oauth/" + name + "/callback"
				})
			log.Printf("oauth tokens: %s", tokenPath)
		}
	}
	if cfg.Links.Enabled {
		secretPath := cfg.Links.SecretPath
		if secretPath == "" {
			secretPath = "data/links-secret"
		}
		storeDir := cfg.Links.StoreDir
		if storeDir == "" {
			storeDir = "data/links"
		}
		vaultDir := cfg.Links.VaultDir
		if vaultDir == "" {
			vaultDir = "data/vault"
		}
		ls, err := links.Open(secretPath, cfg.Links.BaseURL, storeDir, vaultDir, func(format string, args ...any) {
			log.Printf("links: "+format, args...)
		})
		if err != nil {
			log.Printf("links disabled: %v", err)
		} else {
			tl = append(tl, tools.MakeReportLink{S: ls}, tools.MakeFormLink{S: ls})
			linkSvc = ls
			log.Printf("links server: %s/l/ (reports and forms)", cfg.Links.BaseURL)
		}
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
	// Stage 25e: code-made images (charts) as native chat media. The
	// dispatcher resolves the channel at call time, so channels built
	// below are visible to the tool.
	mediaSend := func(ctx context.Context, channelName, userID, mimeType, caption string, data []byte) error {
		for _, ch := range chans {
			if ch.Name() != channelName {
				continue
			}
			s, ok := ch.(interface {
				SendMediaBytes(context.Context, string, string, string, []byte) error
			})
			if !ok {
				return fmt.Errorf("send_chart: channel %s cannot send media", channelName)
			}
			return s.SendMediaBytes(ctx, userID, mimeType, caption, data)
		}
		return fmt.Errorf("send_chart: channel %s is not enabled", channelName)
	}
	tl = append(tl, tools.SendChart{Send: mediaSend})
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
	if linkSvc != nil {
		mounted := false
		for _, ch := range chans {
			if m, ok := ch.(interface{ MountLinks(http.Handler) }); ok {
				m.MountLinks(linkSvc.Handler())
				mounted = true
			}
		}
		if !mounted {
			log.Printf("links: no channel listener to mount on - links will not be reachable")
		}
	}
	if oauthHandler != nil {
		mounted := false
		for _, ch := range chans {
			if m, ok := ch.(interface{ MountOAuth(http.Handler) }); ok {
				m.MountOAuth(oauthHandler)
				mounted = true
			}
		}
		if !mounted {
			log.Printf("oauth: no channel listener to mount on - connect URLs will not be reachable")
		}
	}
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
