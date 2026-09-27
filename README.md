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

```bash
git clone https://github.com/FiveTechSoft/FiveAgent.git
cd FiveAgent
cp .env.example .env   # set your model endpoint and keys
docker compose up -d
```

Then link a channel (WhatsApp / Telegram / iMessage) from the setup wizard at `http://localhost:8000/setup`.

Prefer bare metal? FiveAgent is written in Go and builds to a single static binary - no runtime needed:

```bash
go build ./cmd/fiveagent
./fiveagent
```

## Configuration

One file, `fiveagent.yml` (or env vars). Everything important is a config line, never a code change:

```yaml
model:
  provider: openai-compatible     # any OpenAI-compatible endpoint
  base_url: http://localhost:11434/v1   # Ollama, llama.cpp, vLLM, OpenAI, DeepSeek...
  name: llama3.1                  # the model you choose
channels:
  whatsapp: { enabled: true }
  telegram: { enabled: true, bot_token: ${TELEGRAM_BOT_TOKEN} }
  imessage: { enabled: false }
memory:
  postgres: postgres://fiveagent:fiveagent@db:5432/fiveagent
```

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
