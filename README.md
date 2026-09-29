# FiveAgent

Your personal AI agent. Open source, self-hosted, model-agnostic.

FiveAgent wants to be a personal assistant that lives in your messaging apps and works for you. It runs on your own hardware with Docker, talks to the model **you** choose (local or commercial), and keeps your data in your own database. It's brand new: what works today and what's still missing is written below, plainly.

## Why FiveAgent

- **Self-host for real.** One `docker compose up`. Postgres and the agent on your machine. No mandatory cloud accounts, no vendor lock-in.
- **Model freedom.** Any OpenAI-compatible endpoint: local models via Ollama / llama.cpp / vLLM, or commercial APIs (OpenAI, DeepSeek, Anthropic via adapter). Change model by editing one config line.
- **Chat-first.** WhatsApp (official Cloud API) and Telegram work today; iMessage is on the roadmap. You talk to it where you already talk.
- **Your data stays yours.** Memory in your own Postgres. Data only leaves your machine where you decide: to the model you configured and to the messaging platforms you connect (e.g. Meta's servers when you use the official WhatsApp API).
- **Open by design.** MIT license, semantic releases, public roadmap. Community PRs welcome and actually reviewed.

## Where FiveAgent runs

On a machine you control - your Windows PC or your own server, never on
someone else's cloud. Three things live there:

- `fiveagent` (`fiveagent.exe` on Windows): the agent itself, listening
  on `localhost:8080`.
- `cloudflared`: the only piece that faces the internet. It runs next
  to the agent on the same machine and forwards Meta's webhooks to
  `localhost:8080`. On Windows, install the MSI, then
  `cloudflared service install <tunnel-token>` so it starts with the PC.
- Ollama (optional, for local models): same machine,
  `localhost:11434`. It has no auth: never expose it publicly, keep it
  on localhost.

Moving to another machine one day? Install FiveAgent and cloudflared
there and start the tunnel with the same token: the public URL and the
Meta webhook stay the same, because they live at Cloudflare, not on
your box.

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

WhatsApp: follow [docs/whatsapp.md](docs/whatsapp.md) - guía en español, paso a paso, con las trampas reales ya documentadas.

![Real conversation: a phone talks to FiveAgent on WhatsApp, answered by DeepSeek](docs/images/1-2-whatsapp-e2e-real.jpg)

![Real conversation: FiveAgent answers on WhatsApp using a local model (Ollama qwen3.8:27b) on a home PC, no cloud](docs/images/1-3-whatsapp-ollama-pc-real.jpg) Telegram: [docs/telegram.md](docs/telegram.md), five minutes with @BotFather, no tunnel needed. iMessage is coming next.

Something doesn't work? Open an issue: https://github.com/FiveTechSoft/FiveAgent/issues - tell us your operating system and the exact error message.

## Architecture

One service, three layers. See [docs/design.md](docs/design.md) for the full design.

- **Channels** - thin adapters that normalize messages into one event format. WhatsApp and Telegram are implemented; iMessage is planned.
- **Core** - a single Go binary running the agent loop: conversation memory and model calls. Provider-agnostic.
- **Tools** - planned: web search, email, calendar, files, and a sandboxed browser (Playwright sidecar container, already in docker compose).

## Works today

- Single Go binary, no runtime. Docker compose brings up the agent, Postgres and a Playwright sidecar.
- WhatsApp channel via the official Cloud API, verified end-to-end in a live install ([guía en español paso a paso](docs/whatsapp.md)). Telegram channel via long polling (no public URL needed).
- Conversation memory in Postgres: the agent remembers the last 20 messages of each chat.
- Links, not walls of text: long answers go out as signed, PIN-protected links to clean pages served by the bot itself (`make_report_link`), and sensitive data (tokens, passwords) is collected with a small form link (`make_form_link`) whose value lands straight in the vault (data/vault), never in the chat or any log (`links.enabled` + `links.base_url` in the yml).
- Cron scheduler: reminders and recurring automations in natural language (`schedule_job` tool), delivered back to your WhatsApp/Telegram when they fire (`cron.enabled` in the yml); every job is auditable in data/jobs.json (what ran, when, what it sent).
- Durable delivery ledger: every reply is recorded before sending (data/deliveries.json), so a crash mid-send never silently loses it - on the next start, pending replies are redelivered once with a visible recovered marker. Attempts are capped (5) and replies undelivered after 24 h expire instead of arriving confusingly late.
- Any OpenAI-compatible model by config: Ollama, llama.cpp, vLLM, OpenAI, DeepSeek.
- Tool calling: the model can call tools (OpenAI-style function calling). Built-in tools: current date/time, web_search (DuckDuckGo by default, Brave optional), memory save/forget, `run_subtask` (isolated subturn with fresh context for multi-step requests, depth 1), workspace file tools (`read_file` / `write_file` / `edit_file` with true diffs - the tool verifies old_text matches exactly once and returns the applied diff - scoped to a per-user folder with a go-git snapshot before every write, so every edit is undoable; `workspace.enabled` in the yml), a web browser (`browse_page` shows a page as a numbered list of elements, `browser_act` acts by id - fill/click - with every form submit gated behind explicit user confirmation and a per-user audit log where passwords are redacted; `browser.enabled` in the yml), `gmail_search`/`gmail_send` (OAuth-connected Gmail, stage 27a: connect at /oauth/gmail/start, tokens at data/tokens.json 0600, transparent refresh; live verification pending), `send_chart` (renders a small bar/line chart in pure Go and sends it as a native chat image - WhatsApp upload + send by id; send failures surface to the model as errors, never as a silent success), and `run_command`, which executes commands inside a per-user sandbox. Backends: bubblewrap on Linux (CI-tested: no network, only the user's folder writable), AppContainer on Windows (CI-tested, pending live verification on a real PC), Docker fallback. If AppContainer is unavailable the sandbox degrades to Job Objects - RAM cap and timeout only, NO network or filesystem isolation - with a loud WARNING in the log. Run `fiveagent doctor` to see what your machine supports.

## Roadmap

Tracked as GitHub issues and milestones, in this order:

1. ~~Tool calling in the agent loop~~ (done)
2. ~~Telegram channel~~ (done)
3. Per-user sandboxed command execution (all backends verified in CI: bubblewrap on Linux, AppContainer + Job Objects on Windows, Docker fallback; macOS sandbox-exec later)
4. ~~CI with tests (GitHub Actions)~~ (done: ubuntu + windows + macos on every push)
5. First release: v0.0.1
6. Long-term memory (pgvector), secrets encrypted at rest, prompt-injection tests
7. Tools: web search, browser, email, calendar
8. iMessage channel
9. Per-task model routing (planned): each request goes to the model best suited to it (chat to Qwen3-30B, code to Qwen2.5-Coder), with a router classifying requests and the models configurable in fiveagent.yml

Details in [docs/ROADMAP.md](docs/ROADMAP.md).

## Security

- Today: your secrets live in `fiveagent.yml` / environment variables on your machine. Keep that file private (it's in .gitignore).
- The browser sidecar runs in its own container.
- On the roadmap: secrets encrypted at rest (AES-256-GCM) and a prompt-injection test suite in CI. External content will be treated as data, never as instructions.

## Status

Early days. Not ready for production use. Star the repo and watch the releases.

## Contributing

Issues and PRs welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT - see [LICENSE](LICENSE).

Parts of the design are inspired by [OpenInstinct](https://github.com/Merit-Systems/OpenInstinct) (MIT, Merit Systems). Attribution in [NOTICE](NOTICE).
