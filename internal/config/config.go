// Package config loads fiveagent.yml: model endpoint, channels and storage.
package config

import (
	"fmt"
	"os"

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
	// SystemPrompt overrides the agent's built-in persona. Optional; the
	// model identity line is always appended (see agent.SystemPrompt).
	SystemPrompt string `yaml:"system_prompt,omitempty"`
	// Delivery is the durable delivery ledger (stage 19).
	Delivery Delivery `yaml:"delivery,omitempty"`
	// Workspace is the per-user file-tools folder (stage 21).
	Workspace Workspace `yaml:"workspace,omitempty"`
	// Cron is the scheduler for reminders and automations (stage 22).
	Cron Cron `yaml:"cron,omitempty"`
	// Browser is the web browser tool (stage 23).
	Browser Browser `yaml:"browser,omitempty"`
	// Links is the link server for reports and forms (stage 24).
	Links Links `yaml:"links,omitempty"`
}

// UnmarshalYAML defaults Sandbox.Enabled to true: the sandbox protects
// the host from the agent's shell commands, so it should be on unless
// the owner opts out. An explicit enabled: false in the yml wins.
func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	c.Sandbox.Enabled = true
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
	return &c, nil
}
