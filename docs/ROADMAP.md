# FiveAgent roadmap

Ordered, one thing at a time, simple first. Each item is a GitHub issue;
the phases are milestones.

## Phase 1 - v0.1: a talking agent

1. **Tool calling** (done) - the model can call tools through the OpenAI function-calling
   protocol, with a registry to add more. First tool: current date/time.
2. **Telegram channel** - Bot API with long polling. Simplest official API there is.
3. **Per-user sandbox** (working, verified in CI) - the agent can run
   commands for each user inside an isolated environment, via a
   `run_command` tool. Backends, all with green tests on real runners:
   bubblewrap on Linux (no network, host files hidden, one writable folder
   per user, timeout), Windows AppContainer + Job Objects (no network, host
   files unreadable, RAM cap, timeout; plain Job Objects remains as the
   fallback), Docker (no network, RAM/CPU caps). Later: sandbox-exec on
   macOS.
4. **CI** (done) - GitHub Actions: build, vet and tests on every push, on
   ubuntu-latest, windows-latest and macos-latest. First all-green run:
   https://github.com/FiveTechSoft/FiveAgent/actions/runs/36331174095
5. **Unit tests** - model client, WhatsApp webhook, agent loop.
6. **Release v0.0.1** - tag, changelog, prebuilt binaries for Windows/Linux/macOS.

## Phase 2 - v0.2: memory and safety

7. **File-based long-term memory** (planned) - the agent remembers
   beyond the last 20 messages using plain markdown files with git as
   the source of truth: readable, versioned, no extra infrastructure,
   and an LLM reads markdown natively. It grows in stages:
   a. **Minimal memory** - three files: people.md, preferences.md,
      workstreams.md. Empty folders from day one are debt; the
      structure grows only when real use demands it.
   b. **Keyword and alias retrieval** - recall by exact words and
      curated aliases kept in each memory file.
   c. **SQLite FTS5 index** - an embedded full-text index as a
      rebuildable cache. The files stay the source of truth; the index
      is disposable. Database speed, zero infrastructure.
   d. **Organic growth and links** - new files and folders appear when
      they hurt; [[id]] links connect related records (a plain-text
      graph, parsed when needed). A graph or vector database only
      enters if real scale one day demands it.
8. **Secrets at rest** - AES-256-GCM encryption for stored credentials.
9. **Prompt-injection tests** - external content is data, never instructions; CI proves it.
10. **Per-task model routing** (planned) - send each request to the model
   that does it best: conversation to Qwen3-30B, code to Qwen2.5-Coder
   (great at code, weaker prose). Configurable in fiveagent.yml
   (models: chat / code / ...), with a router that classifies each
   request. Several local models share the work, each doing its own job.

## Phase 3 - v0.3: tools

11. **Web search** - pluggable backend, with a self-hosted SearXNG option.
12. **Browser** - drive the Playwright sidecar (already in docker compose).
13. **Email + calendar** - read and act on the user's accounts (OAuth).

## Phase 4 - v0.4: more channels

14. **iMessage** - bridge docs + reference implementation (needs a Mac).
15. **WhatsApp extras** - templates, media, groups.

## Phase 5 - setup that does not need a manual

16. **WhatsApp setup wizard** - a `fiveagent setup whatsapp` command that
    does the Meta configuration through the Graph API for you: check the
    token, register the webhook callback, subscribe the app to the
    `messages` field and to the WhatsApp Business Account
    (`subscribed_apps`), and confirm the phone number id. Idea born from a
    real first-time setup: the Meta panel is confusing enough to stop new
    users. Until this exists, docs/whatsapp.md is the way.

## Principles

- Simple first: one binary, one config file, one command to run.
- Windows first in docs and installers.
- The README only claims what the code does today.
