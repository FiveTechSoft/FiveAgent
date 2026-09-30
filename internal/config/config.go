// Package config loads fiveagent.yml: model endpoint, channels and storage.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Model selects the chat model. Any OpenAI-compatible endpoint works:
// Ollama, llama.cpp, vLLM, OpenAI, DeepSeek, ...
type Model struct {
	Provider string `yaml:"provider"` // "openai-compatible"
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Name     string `yaml:"name"`
	// Timeout caps one model call, in seconds. Default: 120, or 600 for
	// local endpoints (localhost), where large models load into RAM on
	// first use and easily exceed two minutes.
	Timeout int `yaml:"timeout,omitempty"`
	// ChatTemplateKwargs passes arbitrary chat-template keyword arguments
	// (e.g. enable_thinking: false for reasoning models on SGLang/vLLM)
	// in the /v1/chat/completions body. Measured need: a reasoning model
	// can spend the whole token budget on internal thinking and return
	// empty visible content. Only the OpenAI-compatible route carries
	// them - num_thread forces Ollama's native route, so setting both on
	// the same model is a config error (no silent drops).
	ChatTemplateKwargs map[string]any `yaml:"chat_template_kwargs,omitempty"`
	// NumThread caps the inference thread count (Ollama's num_thread).
	// Without it the runner spawns one thread per CPU core and saturates
	// shared hosts even when the process is pinned by affinity. 0 leaves
	// the server default. Honored only through Ollama's native API: when
	// set, requests go to /api/chat instead of /v1/chat/completions, so
	// base_url must end in /v1 (validated at Load).
	NumThread int `yaml:"num_thread,omitempty"`
}

// Sampling holds the stage 11a per-request-kind sampling parameters.
// Small models need precision when they call tools and fluency when
// they chat: ToolTemperature serves tool-calling rounds, ChatTemperature
// the final answer. PresencePenalty pushes small models out of the
// repetition loops they fall into. Unset fields are not sent, so the
// provider's own defaults apply (adopt or drop by evals, never vibes).
type Sampling struct {
	ToolTemperature *float64 `yaml:"tool_temperature,omitempty"`
	ChatTemperature *float64 `yaml:"chat_temperature,omitempty"`
	PresencePenalty *float64 `yaml:"presence_penalty,omitempty"`
}

// Channel is one messaging channel's settings. The fields used depend on
// the adapter: telegram uses bot_token; whatsapp (official Cloud API) uses
// access_token, phone_number_id, verify_token and listen_addr.
type Channel struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token,omitempty"`
	// WhatsApp Cloud API
	AccessToken   string `yaml:"access_token,omitempty"`
	PhoneNumberID string `yaml:"phone_number_id,omitempty"`
	VerifyToken   string `yaml:"verify_token,omitempty"`
	AppSecret     string `yaml:"app_secret,omitempty"`  // enables X-Hub-Signature-256 checks
	ListenAddr    string `yaml:"listen_addr,omitempty"` // webhook listen address, default :8080
	// AllowedSenders limits who the bot answers: WhatsApp phone numbers as
	// they arrive (e.g. "34600123456"), Telegram chat IDs (e.g. "123456789").
	// Empty means allow everyone (a warning is logged at startup).
	AllowedSenders []string `yaml:"allowed_senders,omitempty"`
	// Reactions: "status" (default) reacts to inbound messages - eyes while
	// working, check when the reply lands, warning on failure; "off"
	// disables reactions. WhatsApp only for now.
	Reactions string `yaml:"reactions,omitempty"`
	// Debounce joins rapid message bursts from one sender into a single
	// agent turn: people write in bursts ("hola" / "una pregunta" / "...")
	// and the natural reply reads them together. Value is a Go duration
	// ("3s", "1500ms"); empty defaults to 3s; "off" answers every message
	// on arrival (the old parallel behavior).
	Debounce string `yaml:"debounce,omitempty"`
	// WhatsApp media processing (stage 25): transcriber_url points at a
	// whisper.cpp server (voice notes -> text), describer_url at an
	// OpenAI-compatible vision endpoint (images -> text). Empty disables
	// that direction; inbound media is then announced without content.
	TranscriberURL string `yaml:"transcriber_url,omitempty"`
	DescriberURL   string `yaml:"describer_url,omitempty"`
	DescriberModel string `yaml:"describer_model,omitempty"`
	DescriberKey   string `yaml:"describer_key,omitempty"`
	// Outbound voice notes (stage 25d): tts_url points at an
	// OpenAI-compatible /v1/audio/speech endpoint (openedai-speech for
	// Piper, kokoro-fastapi for Kokoro). Empty disables voice replies;
	// replies then go out as text as before.
	TTSURL   string `yaml:"tts_url,omitempty"`
	TTSModel string `yaml:"tts_model,omitempty"`
	TTSVoice string `yaml:"tts_voice,omitempty"`
	TTSKey   string `yaml:"tts_key,omitempty"`
	// Inbound video analysis (stage 25c): ffmpeg is an external tool;
	// ffmpeg_path overrides the binary location, empty means PATH. When
	// ffmpeg is available AND the transcriber/describer above are set,
	// inbound videos arrive as transcript + frame descriptions.
	FFmpegPath string `yaml:"ffmpeg_path,omitempty"`
}

// Memory holds the storage settings. JSON is a file-backed store for local
// prototypes; Postgres for real deployments. If JSON is set, it wins.
type Memory struct {
	Postgres string `yaml:"postgres"`
	JSON     string `yaml:"json,omitempty"`
	// Knowledge is the long-term memory folder (markdown + git). When
	// set, the agent recalls from it every turn and gets save_memory /
	// forget_memory tools to curate it. Empty disables long-term memory.
	Knowledge string `yaml:"knowledge,omitempty"`
	// AutoIndex absorbs every non-trivial turn into a per-sender
	// memory scope in the background (stage 7n). Default true when
	// knowledge is set; auto_index: false opts out. Cost: one small
	// model call per non-trivial turn.
	AutoIndex bool `yaml:"auto_index"`
	// ConsolidateIdleMin is the stage 7l idle-time consolidation: after
	// this many minutes without a turn, a background pass merges
	// near-duplicate memory facts. 0 disables it.
	ConsolidateIdleMin int `yaml:"consolidate_idle_minutes,omitempty"`
}

// Sandbox holds the per-user isolated execution settings. Enabled by
// default (see Config.UnmarshalYAML); set enabled: false to opt out.
// When enabled, the agent gets a run_command tool whose commands run
// inside a per-user sandbox (see internal/sandbox). If no backend is
// available on the machine, startup logs "sandbox disabled: ..." and
// continues without the tool.
type Sandbox struct {
	Enabled bool   `yaml:"enabled"`
	Backend string `yaml:"backend,omitempty"`    // auto | bubblewrap (Linux) | appcontainer / jobobject (Windows) | docker
	Root    string `yaml:"root,omitempty"`       // default data/sandbox
	Timeout int    `yaml:"timeout,omitempty"`    // seconds per command, default 30
	MaxRAM  int    `yaml:"max_ram_mb,omitempty"` // docker / jobobject only, default 512
	Image   string `yaml:"image,omitempty"`      // docker only, default alpine
}

// Links holds the link-server settings (stage 24): signed,
// PIN-protected report and form links served by the bot itself.
type Links struct {
	Enabled bool `yaml:"enabled"`
	// BaseURL is the public base the bot is reached on (the tunnel
	// URL); links are minted under it. Required when enabled.
	BaseURL string `yaml:"base_url,omitempty"`
	// SecretPath is the HMAC signing key file; default
	// data/links-secret (created on first run).
	SecretPath string `yaml:"secret_path,omitempty"`
	// StoreDir holds report pages and link records; default data/links.
	StoreDir string `yaml:"store_dir,omitempty"`
	// VaultDir receives form submissions; default data/vault.
	VaultDir string `yaml:"vault_dir,omitempty"`
}

// Secrets holds encryption-at-rest settings (stage 8).
type Secrets struct {
	// KeyFile is the master key file (default data/master.key,
	// created 0600 on first use). Documented limitation: a key file
	// on the same disk as the data protects copies of the data
	// (backups, sync clients), not the live machine.
	KeyFile string `yaml:"key_file,omitempty"`
	// KeyEnv names the environment variable holding the master key
	// (base64 or hex, 32 bytes; default FIVEAGENT_MASTER_KEY). The
	// environment is the strongest source: it keeps the key out of
	// the filesystem entirely. Set KeyEnv to "off" to disable
	// encryption explicitly.
	KeyEnv string `yaml:"key_env,omitempty"`
}

// Browser holds the web-browser settings (stage 23): numbered-element
// pages, by-id actions, gated submits, per-user audit log.
type Browser struct {
	Enabled bool `yaml:"enabled"`
	// AuditDir receives one audit log per user; default data/browser-audit.
	AuditDir string `yaml:"audit_dir,omitempty"`
}

// Cron holds the scheduler settings (stage 22): one-shot reminders and
// recurring automations, delivered back to the originating channel.
type Cron struct {
	Enabled bool `yaml:"enabled"`
	// Path is the jobs JSON file; default data/jobs.json.
	Path string `yaml:"path,omitempty"`
}

// Identity holds the cross-channel linking settings (stage 36):
// channel identities resolve to one user identity through an
// explicit code flow, never guessed; unlinked senders stay separate.
// Enabled by default - inert until a user links two channels; set
// enabled: false to opt out.
// Trajectory is the opt-in trajectory logger (stage 12 of
// docs/ROADMAP.md): every turn is recorded redacted as a JSONL
// trajectory, in the message shape the fine-tuning dataset (stage 13)
// consumes. Rotation is bounded.
type Trajectory struct {
	Enabled bool `yaml:"enabled,omitempty"`
	// Dir holds the rotating trajectories.jsonl (default
	// data/trajectories).
	Dir string `yaml:"dir,omitempty"`
	// MaxMB caps each rotating file (default 10).
	MaxMB int `yaml:"max_mb,omitempty"`
	// MaxFiles caps how many rotated files are kept (default 5).
	MaxFiles int `yaml:"max_files,omitempty"`
}

// MCP is the local MCP server for external agents such as the
// owner's OpenCode (stage 33 of docs/ROADMAP.md). Token-authenticated,
// loopback by default: it exposes fiveagent_run_command (executes
// inside the sandbox, never outside it) and fiveagent_read_file
// (confined to the workspace folder).
type MCP struct {
	Enabled bool `yaml:"enabled,omitempty"`
	// ListenAddr defaults to 127.0.0.1:8090. Keep it on loopback
	// unless the transport in front adds its own authentication.
	ListenAddr string `yaml:"listen_addr,omitempty"`
	// Token is the required bearer token; the server refuses to
	// start without one.
	Token string `yaml:"token,omitempty"`
	// UserKey selects the sandbox folder for MCP calls (default
	// "mcp").
	UserKey string `yaml:"user_key,omitempty"`
}

type Identity struct {
	Enabled bool `yaml:"enabled"`
	// Path is the identities JSON file; default data/identities.json.
	Path string `yaml:"path,omitempty"`
}

// Proactive holds the source-subscription settings (stage 35):
// adapters notify events, matching subscriptions wake the agent, and
// replies land through the delivery ledger (stage 19). Time-based
// wakes stay with cron (stage 22).
type Proactive struct {
	Enabled bool `yaml:"enabled"`
	// Path is the subscriptions JSON file; default data/subscriptions.json.
	Path string `yaml:"path,omitempty"`
}

// Workspace holds the file-tools settings (stage 21): the agent gets
// read_file / write_file / edit_file scoped to a per-user folder, with
// go-git snapshots before every write so every edit is undoable.
type Workspace struct {
	Enabled bool `yaml:"enabled"`
	// Root is the base folder; each user gets Root/<channel>-<userID>/.
	// Default data/workspace.
	Root string `yaml:"root,omitempty"`
}

// Delivery holds the durable delivery ledger settings (stage 19):
// every outbound reply is recorded before sending, and after a crash
// the pending ones are redelivered once with a recovered marker.
type Delivery struct {
	// Path is the ledger JSON file; default data/deliveries.json.
	Path string `yaml:"path,omitempty"`
}

// WebSearch configures the optional web_search tool. Disabled by
// default. Providers: duckduckgo (default, no API key, may rate-limit
// under heavy use) and brave (needs api_key, the reliable upgrade).
type WebSearch struct {
	Enabled    bool   `yaml:"enabled"`
	Provider   string `yaml:"provider,omitempty"`    // duckduckgo (default) | brave
	APIKey     string `yaml:"api_key,omitempty"`     // brave only
	MaxResults int    `yaml:"max_results,omitempty"` // default 5, hard cap 10
}

// Config is the root of fiveagent.yml.
type Config struct {
	Model    Model              `yaml:"model"`
	Channels map[string]Channel `yaml:"channels"`
	Memory   Memory             `yaml:"memory"`
	Sandbox  Sandbox            `yaml:"sandbox,omitempty"`
	// WebSearch is the optional web_search tool (DuckDuckGo by default).
	WebSearch WebSearch `yaml:"web_search,omitempty"`
	// Coder is an optional second model for code-heavy requests. When set,
	// the agent picks it for messages that look like code and keeps
	// Model for everything else. It inherits model.base_url, model.provider
	// and model.api_key when omitted.
	Coder Model `yaml:"coder,omitempty"`
	// Sampling is the stage 11a per-request-kind sampling. Unset fields
	// are not sent; the provider's defaults apply.
	Sampling Sampling `yaml:"sampling,omitempty"`
	// SystemPrompt overrides the agent's built-in persona. Optional; the
	// model identity line is always appended (see agent.SystemPrompt).
	SystemPrompt string `yaml:"system_prompt,omitempty"`
	// Delivery is the durable delivery ledger (stage 19).
	Delivery Delivery `yaml:"delivery,omitempty"`
	// Workspace is the per-user file-tools folder (stage 21).
	Workspace Workspace `yaml:"workspace,omitempty"`
	// Cron is the scheduler for reminders and automations (stage 22).
	Cron Cron `yaml:"cron,omitempty"`
	// Proactive is the source-subscription wake layer (stage 35).
	Proactive Proactive `yaml:"proactive,omitempty"`
	// Identity is cross-channel conversation unification (stage 36).
	Identity Identity `yaml:"identity,omitempty"`
	// MCP is the local MCP server for external agents (stage 33).
	MCP MCP `yaml:"mcp,omitempty"`
	// Trajectory is the opt-in turn recorder (stage 12).
	Trajectory Trajectory `yaml:"trajectory,omitempty"`
	// Browser is the web browser tool (stage 23).
	Browser Browser `yaml:"browser,omitempty"`
	// Links is the link server for reports and forms (stage 24).
	Links Links `yaml:"links,omitempty"`

	// Secrets is encryption at rest for stored credentials (stage 8).
	Secrets Secrets `yaml:"secrets,omitempty"`
	// Integrations are the OAuth services of stage 27.
	Integrations Integrations `yaml:"integrations,omitempty"`
}

// Integrations groups the OAuth service settings.
type Integrations struct {
	Gmail    GmailIntegration    `yaml:"gmail,omitempty"`
	Calendar CalendarIntegration `yaml:"calendar,omitempty"`
	Drive    DriveIntegration    `yaml:"drive,omitempty"`
	Slack    SlackIntegration    `yaml:"slack,omitempty"`
	GitHub   GitHubIntegration   `yaml:"github,omitempty"`
}

// GmailIntegration is the Gmail OAuth app + token store (stage 27a).
type GmailIntegration struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	// RedirectURL is the public base URL the webhook listener is
	// reachable at (e.g. "https://bot.example.com"); the callback path
	// /oauth/gmail/callback is appended to it.
	RedirectURL string `yaml:"redirect_url,omitempty"`
	// TokenPath is the OAuth token store file (default
	// data/tokens.json). NOT encrypted at rest - that is roadmap
	// stage 8.
	TokenPath string `yaml:"token_path,omitempty"`
}

// CalendarIntegration is the Google Calendar OAuth app (stage 27b).
// Same fields as Gmail; the connect URL is /oauth/calendar/start.
type CalendarIntegration struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	RedirectURL  string `yaml:"redirect_url,omitempty"`
	TokenPath    string `yaml:"token_path,omitempty"`
}

// DriveIntegration is the Google Drive OAuth app (stage 27c). Same
// fields as Gmail; the connect URL is /oauth/drive/start. Scope is
// drive.file: the agent only sees files it created.
type DriveIntegration struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	RedirectURL  string `yaml:"redirect_url,omitempty"`
	TokenPath    string `yaml:"token_path,omitempty"`
}

// SlackIntegration is the Slack OAuth app (stage 27d). Same fields as
// Gmail; the connect URL is /oauth/slack/start. Bot tokens do not
// expire, so this provider never exercises the refresh path.
type SlackIntegration struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	RedirectURL  string `yaml:"redirect_url,omitempty"`
	TokenPath    string `yaml:"token_path,omitempty"`
}

// GitHubIntegration is the GitHub OAuth app (stage 27e). Same fields
// as Gmail; the connect URL is /oauth/github/start. OAuth-app tokens
// do not expire, so this provider never exercises the refresh path.
type GitHubIntegration struct {
	Enabled      bool   `yaml:"enabled"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	RedirectURL  string `yaml:"redirect_url,omitempty"`
	TokenPath    string `yaml:"token_path,omitempty"`
}

// UnmarshalYAML defaults Sandbox.Enabled to true: the sandbox protects
// the host from the agent's shell commands, so it should be on unless
// the owner opts out. An explicit enabled: false in the yml wins.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	c.Sandbox.Enabled = true
	c.Memory.AutoIndex = true
	c.Identity.Enabled = true
	type plain Config
	return value.Decode((*plain)(c))
}

// Load reads path (default ./fiveagent.yml). Environment variables in the
// file, ${VAR} or $VAR, are expanded before parsing, so secrets can stay
// out of the yml (access_token: ${WHATSAPP_ACCESS_TOKEN}). An unset
// variable expands to the empty string.
func Load(path string) (*Config, error) {
	if path == "" {
		path = "fiveagent.yml"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	b = []byte(os.ExpandEnv(string(b)))
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Model.BaseURL == "" || c.Model.Name == "" {
		return nil, fmt.Errorf("model.base_url and model.name are required")
	}
	if c.Coder.Name != "" && c.Coder.BaseURL == "" {
		c.Coder.BaseURL = c.Model.BaseURL
	}
	if c.Coder.Name != "" && c.Coder.Provider == "" {
		c.Coder.Provider = c.Model.Provider
	}
	if c.Coder.Name != "" && c.Coder.APIKey == "" {
		c.Coder.APIKey = c.Model.APIKey
	}
	if c.Coder.Name != "" && c.Coder.NumThread == 0 {
		c.Coder.NumThread = c.Model.NumThread
	}
	if c.Coder.Name != "" && c.Coder.ChatTemplateKwargs == nil {
		c.Coder.ChatTemplateKwargs = c.Model.ChatTemplateKwargs
	}
	for _, m := range []struct {
		label string
		m     Model
	}{{"model", c.Model}, {"coder", c.Coder}} {
		if m.m.NumThread > 0 && !strings.HasSuffix(m.m.BaseURL, "/v1") {
			return nil, fmt.Errorf("%s.num_thread is honored only through Ollama's native API; %s.base_url must end in /v1 so the native endpoint can be derived", m.label, m.label)
		}
		if m.m.NumThread > 0 && len(m.m.ChatTemplateKwargs) > 0 {
			return nil, fmt.Errorf("%s sets both num_thread (Ollama native route) and chat_template_kwargs (/v1 route); they travel on separate paths, keep one per model", m.label)
		}
		for _, t := range []*float64{c.Sampling.ToolTemperature, c.Sampling.ChatTemperature} {
			if t != nil && (*t < 0 || *t > 2) {
				return nil, fmt.Errorf("sampling temperatures must be between 0 and 2, got %g", *t)
			}
		}
		if p := c.Sampling.PresencePenalty; p != nil && (*p < -2 || *p > 2) {
			return nil, fmt.Errorf("sampling.presence_penalty must be between -2 and 2, got %g", *p)
		}
	}
	return &c, nil
}
