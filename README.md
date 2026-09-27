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

FiveAgent needs just two things: somewhere to run (Docker or Go) and a model to talk to (one you run yourself, like Ollama, or a paid API like OpenAI or DeepSeek).

### Step 1. Install what you need

Pick ONE:
- **Docker Desktop** (recommended, easiest): https://www.docker.com/products/docker-desktop - runs everything for you: the agent, its database and its browser.
- **Go** (if you prefer a single program file): https://go.dev/dl - version 1.24 or newer.

If you want a free local model, also install **Ollama**: https://ollama.com - then run `ollama pull llama3.1` once.

### Step 2. Download FiveAgent

```bash
git clone https://github.com/FiveTechSoft/FiveAgent
cd FiveAgent
```

No git? On the repo page click the green **Code** button and then **Download ZIP**, and unzip it.

### Step 3. Create your settings file

Make your own copy of the example settings:

```bash
# Windows (type this in cmd or PowerShell)
copy fiveagent.yml.example fiveagent.yml

# Mac / Linux
cp fiveagent.yml.example fiveagent.yml
```

Open `fiveagent.yml` with any text editor (Notepad works). The only part you must touch is `model`:

```yaml
model:
  base_url: http://localhost:11434/v1   # where the model lives
  name: llama3.1                        # which model to use
  api_key: ""                           # empty for Ollama; your key for OpenAI/DeepSeek
```

- Using Ollama? Leave it as shown above.
- Using OpenAI? Set `base_url: https://api.openai.com/v1`, `name: gpt-4o-mini` and paste your API key.
- Using DeepSeek? Set `base_url: https://api.deepseek.com/v1`, `name: deepseek-chat` and paste your API key.

### Step 4. Start it

With Docker:

```bash
docker compose up --build
```

Or with Go:

```bash
go build -o fiveagent ./app/fiveagent
./fiveagent
```

### Step 5. Talk to it

WhatsApp: follow [docs/whatsapp.md](docs/whatsapp.md) - it walks you through the free Meta test number step by step. Telegram and iMessage are coming next.

Something doesn't work? Open an issue: https://github.com/FiveTechSoft/FiveAgent/issues - tell us your operating system and the exact error message.

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
