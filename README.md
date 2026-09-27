# FiveAgent

Your personal AI agent. Open source, self-hosted, model-agnostic.

FiveAgent is a personal assistant that lives in your messaging apps and works for you: it remembers what matters, uses tools on your behalf, and answers on WhatsApp, Telegram and iMessage. It runs on your own hardware with Docker, talks to the model **you** choose (local or commercial), and keeps your data in your own database.

## Why FiveAgent

- **Self-host for real.** One `docker compose up`. Postgres and the agent on your machine. No mandatory cloud accounts, no vendor lock-in.
- **Model freedom.** Any OpenAI-compatible endpoint: local models via Ollama / llama.cpp / vLLM, or commercial APIs (OpenAI, DeepSeek, Anthropic via adapter). Change model by editing one config line.
- **Chat-first.** First-class channels: WhatsApp, Telegram and iMessage. You talk to it where you already talk.
- **Your data stays yours.** Memory in your own Postgres (+ pgvector). Secrets encrypted at rest; the agent never sees raw credentials.
- **Open by design.** MIT license, semantic releases, public roadmap. Community PRs welcome and actually reviewed.

## Quick start

### Prerequisites

One of:
- **Docker Desktop** (easiest: runs the agent, Postgres and the browser sidecar for you), or
- **Go 1.24+** (builds a single binary; you run Postgres yourself or use Docker just for it).

### 1. Clone and configure

```bash
git clone https://github.com/FiveTechSoft/FiveAgent
cd FiveAgent
```

Copy the example config and edit it:

```bash
# Windows (cmd or PowerShell)
copy fiveagent.yml.example fiveagent.yml

# Linux / macOS
cp fiveagent.yml.example fiveagent.yml
```

Open `fiveagent.yml` and set the model you want to use:

- `model.base_url` - the OpenAI-compatible endpoint. `http://localhost:11434/v1` for Ollama, `https://api.openai.com/v1` for OpenAI, `https://api.deepseek.com/v1` for DeepSeek.
- `model.name` - the model, e.g. `llama3.1`, `gpt-4o-mini`, `deepseek-chat`.
- `model.api_key` - empty for local Ollama; your API key for commercial providers.
- `channels` - enable WhatsApp / Telegram / iMessage. WhatsApp needs the Meta app fields, see [docs/whatsapp.md](docs/whatsapp.md).
- `memory.postgres` - where to store memory. The docker compose below already brings Postgres up with these exact values, so with Docker you can leave it as is.

### 2. Run it

With Docker:

```bash
docker compose up --build
```

Or with Go (needs a Postgres reachable at the DSN in `fiveagent.yml`):

```bash
go build -o fiveagent ./app/fiveagent
./fiveagent
```

### 3. Link a channel

WhatsApp: follow [docs/whatsapp.md](docs/whatsapp.md). Telegram and iMessage: coming next (see the roadmap in [docs/design.md](docs/design.md)).

## Architecture

One service, three layers. See [docs/design.md](docs/design.md) for the full design.

- **Channels** - thin adapters (WhatsApp, Telegram, iMessage) that normalize messages into one event format.
- **Core** - a single Go binary running the agent loop: memory, tools, planning. Provider-agnostic.
- **Tools** - web search, email, calendar, files, and a sandboxed browser (Playwright in a sidecar container).

## Security

- Secrets stored encrypted at rest (AES-256-GCM); tools receive opaque handles.
- Prompt-injection test suite in CI. External content is data, never instructions.
- The browser tool runs in an isolated container.

## Status

Early days. Not ready for production use. Star the repo and watch the releases.

## Contributing

Issues and PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT - see [LICENSE](LICENSE).

Parts of the design are inspired by [OpenInstinct](https://github.com/Merit-Systems/OpenInstinct) (MIT, Merit Systems). Attribution in [NOTICE](NOTICE).
