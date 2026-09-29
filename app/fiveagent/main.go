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
	"github.com/FiveTechSoft/FiveAgent/internal/github"
	"github.com/FiveTechSoft/FiveAgent/internal/google"
	"github.com/FiveTechSoft/FiveAgent/internal/links"
	"github.com/FiveTechSoft/FiveAgent/internal/memory"
	"github.com/FiveTechSoft/FiveAgent/internal/model"
	"github.com/FiveTechSoft/FiveAgent/internal/oauth"
	"github.com/FiveTechSoft/FiveAgent/internal/sandbox"
	"github.com/FiveTechSoft/FiveAgent/internal/identity"
	"github.com/FiveTechSoft/FiveAgent/internal/proactive"
	"github.com/FiveTechSoft/FiveAgent/internal/sched"
	"github.com/FiveTechSoft/FiveAgent/internal/secrets"
	"github.com/FiveTechSoft/FiveAgent/internal/slack"
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
	var core *agent.Agent
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
	// Stage 35: source subscriptions that wake the agent. Created
	// before the registry so the model gets the subscribe tools; the
	// runner closure resolves core lazily (built below) and deliver
	// resolves channels lazily, like cron.
	var pm *proactive.Manager
	if cfg.Proactive.Enabled {
		subPath := cfg.Proactive.Path
		if subPath == "" {
			subPath = "data/subscriptions.json"
		}
		runner := func(ctx context.Context, channelName, userID, prompt string) (string, error) {
			if core == nil {
				return "", fmt.Errorf("proactive: agent not ready")
			}
			return core.Handle(ctx, channelName, userID, prompt)
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
					return fmt.Errorf("proactive: channel %s cannot deliver", channelName)
				}
				return d.Deliver(ctx, userID, text)
			}
			return fmt.Errorf("proactive: channel %s is not enabled", channelName)
		}
		var err error
		pm, err = proactive.Open(subPath, runner, deliver)
		if err != nil {
			log.Printf("proactive disabled: %v", err)
			pm = nil
		} else {
			tl = append(tl, tools.Subscribe{Subs: pm}, tools.Subscriptions{Subs: pm},
				tools.PauseSubscription{Subs: pm}, tools.Unsubscribe{Subs: pm})
			log.Printf("proactive subscriptions: %s", subPath)
		}
	}
	// Stage 36: cross-channel identity links. Inert until a user
	// explicitly links two channels; the tools let the model start
	// the flow and audit it.
	var ids *identity.Store
	if cfg.Identity.Enabled {
		idPath := cfg.Identity.Path
		if idPath == "" {
			idPath = "data/identities.json"
		}
		var err error
		ids, err = identity.Open(idPath)
		if err != nil {
			log.Printf("identity linking disabled: %v", err)
			ids = nil
		} else {
			tl = append(tl, tools.LinkChannel{IDs: ids}, tools.UnlinkChannel{IDs: ids},
				tools.LinkedChannels{IDs: ids})
			log.Printf("identity linking: %s", idPath)
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
			tokenPath = cfg.Integrations.Slack.TokenPath
		}
		if tokenPath == "" {
			tokenPath = cfg.Integrations.GitHub.TokenPath
		}
		if tokenPath == "" {
			tokenPath = "data/tokens.json"
		}
		// Stage 8: encryption at rest. The environment is the
		// strongest key source (key never touches the filesystem);
		// the key file keeps zero-setup operation. "off" disables.
		var cipher *secrets.Cipher
		{
			keyEnv := cfg.Secrets.KeyEnv
			if keyEnv == "" {
				keyEnv = "FIVEAGENT_MASTER_KEY"
			}
			if keyEnv != "off" {
				if raw := os.Getenv(keyEnv); raw != "" {
					key, err := secrets.ParseKey(raw)
					if err != nil {
						log.Printf("secrets: %s: %v - token store stays PLAINTEXT", keyEnv, err)
					} else if c, err := secrets.NewCipher(key); err != nil {
						log.Printf("secrets: %v - token store stays PLAINTEXT", err)
					} else {
						cipher = c
						log.Printf("secrets: token store encrypted at rest (key from %s)", keyEnv)
					}
				} else {
					keyFile := cfg.Secrets.KeyFile
					if keyFile == "" {
						keyFile = "data/master.key"
					}
					if key, err := secrets.LoadOrCreateKeyFile(keyFile); err != nil {
						log.Printf("secrets: key file %s: %v - token store stays PLAINTEXT", keyFile, err)
					} else if c, err := secrets.NewCipher(key); err != nil {
						log.Printf("secrets: %v - token store stays PLAINTEXT", err)
					} else {
						cipher = c
						log.Printf("secrets: token store encrypted at rest (key file %s - protects copies, not the live disk)", keyFile)
					}
				}
			}
		}
		store := &oauth.TokenStore{Path: tokenPath, Cipher: cipher}
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
		if cfg.Integrations.Slack.Enabled {
			ocfg := oauth.Config{
				ClientID:     cfg.Integrations.Slack.ClientID,
				ClientSecret: cfg.Integrations.Slack.ClientSecret,
				AuthURL:      slack.AuthURL,
				TokenURL:     slack.TokenURL,
				Scopes:       []string{"channels:read", "channels:history", "chat:write"},
			}
			slackFor := func(ctx context.Context) (*slack.Client, error) {
				c, err := oauth.Client(ctx, ocfg, store, "slack")
				if err != nil {
					return nil, err
				}
				return &slack.Client{HTTP: c}, nil
			}
			tl = append(tl, tools.SlackChannels{Client: slackFor}, tools.SlackRead{Client: slackFor}, tools.SlackSend{Client: slackFor})
			providers["slack"] = ocfg
			redirects["slack"] = cfg.Integrations.Slack.RedirectURL
			log.Printf("slack integration: connect at /oauth/slack/start")
		}
		if cfg.Integrations.GitHub.Enabled {
			ocfg := oauth.Config{
				ClientID:     cfg.Integrations.GitHub.ClientID,
				ClientSecret: cfg.Integrations.GitHub.ClientSecret,
				AuthURL:      github.AuthURL,
				TokenURL:     github.TokenURL,
				Scopes:       []string{"repo"},
				// GitHub's token endpoint answers form-encoded
				// unless asked for JSON.
				TokenHeaders: map[string]string{"Accept": "application/json"},
			}
			ghFor := func(ctx context.Context) (*github.Client, error) {
				c, err := oauth.Client(ctx, ocfg, store, "github")
				if err != nil {
					return nil, err
				}
				return &github.Client{HTTP: c}, nil
			}
			tl = append(tl, tools.GitHubRepos{Client: ghFor}, tools.GitHubIssues{Client: ghFor}, tools.GitHubCreateIssue{Client: ghFor})
			providers["github"] = ocfg
			redirects["github"] = cfg.Integrations.GitHub.RedirectURL
			log.Printf("github integration: connect at /oauth/github/start")
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
	core = agent.New(mdl, store, reg, agent.SystemPrompt(cfg))
	if ids != nil {
		core.WithIdentities(ids)
	}
	core.WithSkills(agent.DomainSkill())
	if kn != nil {
		core.WithKnowledge(kn)
		if cfg.Memory.AutoIndex {
			core.WithIndexer(agent.NewIndexer(mdl, cfg.Memory.Knowledge))
			log.Printf("memory auto-indexing: on (per-sender scopes under %s/users/)", cfg.Memory.Knowledge)
		}
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
