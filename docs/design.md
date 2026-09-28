# FiveAgent - Design

Goal: a personal AI agent that is simple to self-host, effective in daily use, and free of vendor lock-in.

## Principles

1. **One command to run.** `docker compose up` gives you a working agent. No cloud accounts required.
2. **Config over code.** Models, channels and tools are enabled in config, not hardcoded.
3. **Your data, your box.** All state in your Postgres. Nothing leaves your machine except the model API calls you configured.
4. **Small core, thin adapters.** The agent loop is small and boring on purpose; channels and tools are plugins.

## Components

### Core (Go)

A single static binary: no runtime, no dependency hell, trivial to ship for Windows, Linux and macOS. The agent loop: receives a normalized message event, gathers context (memory, tools, conversation), calls the configured model, executes tool calls, replies through the channel adapter. Single process; scale-out is out of scope for v1.

### Model layer

One abstraction: an OpenAI-compatible chat endpoint.

- Local: Ollama, llama.cpp, vLLM, LM Studio.
- Commercial: OpenAI, DeepSeek, Groq, Together, Anthropic (via LiteLLM-style adapter shipped as optional extra).

Every model role (main agent, memory summarizer, browser agent) is independently configurable in `fiveagent.yml`. Nothing is fixed in code.

### Channels

Adapters normalize each platform into one `MessageEvent` and one `send` call.

- **WhatsApp** - via the official WhatsApp Cloud API (Meta): webhook in, Graph API out. No ban risk; needs a Meta app, see docs/whatsapp.md.
- **Telegram** - Bot API (long polling by default; webhook optional).
- **iMessage** - via a macOS bridge process (documented; requires a Mac on the network).

### Tools

- Web search (pluggable backend: Brave, Tavily, you.com, SearXNG self-hosted).
- Email and calendar (user's accounts, OAuth where possible).
- Files (local workspace inside the container).
- Browser: Playwright in a dedicated sidecar container, isolated network by default.

### Memory

Postgres + pgvector. Conversation history, long-term facts, and embeddings for recall. No external vector service.

### Secrets

Today: secrets live in fiveagent.yml (keep it chmod 600) or in environment
variables - config.Load expands ${VAR} at load time, so tokens can stay out
of the file. Planned (issue #6): encryption at rest with AES-256-GCM using
a master key from the environment; tools receive opaque identifiers, never
raw secrets.

## Safety

- External content (web pages, emails, messages from others) is treated as untrusted data, never as instructions. Prompt-injection regression tests: planned (issue #7).
- The agent's shell commands run in a per-user sandbox (on by default): bubblewrap on Linux, AppContainer on Windows with an explicit Job Objects degradation warning, Docker as fallback. Run `fiveagent doctor` to see what a machine supports.

## Roadmap (v0)

- [x] Repo skeleton + docker compose (agent + Postgres + browser sidecar)
- [x] Model layer with Ollama and OpenAI-compatible configs
- [x] Telegram channel (Bot API, long polling; unit-tested, pending first live run)
- [x] WhatsApp channel (Cloud API; verified in a real install)
- [x] Memory (conversation history + recall; JSON and Postgres stores, long-term markdown notes)
- [x] Web search tool (DuckDuckGo/Brave; unit-tested, pending first live run)
- [ ] Browser tool (Playwright sidecar is in docker compose but not wired to the agent yet)
- [ ] iMessage bridge (docs + reference implementation)
- [ ] v0.1.0 release with changelog
