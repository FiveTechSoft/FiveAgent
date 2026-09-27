# FiveAgent roadmap

Ordered, one thing at a time, simple first. Each item is a GitHub issue;
the phases are milestones.

## Phase 1 - v0.1: a talking agent

1. **Tool calling** (done) - the model can call tools through the OpenAI function-calling
   protocol, with a registry to add more. First tool: current date/time.
2. **Telegram channel** - Bot API with long polling. Simplest official API there is.
3. **Per-user sandbox** (in progress) - the agent can run commands for each
   user inside an isolated environment, via a `run_command` tool. Backends:
   bubblewrap on Linux (working, tested: no network, host files hidden, one
   writable folder per user, timeout), Windows Job Objects (compiles; RAM cap
   and process-tree kill; filesystem/network isolation via AppContainer is the
   next step - pending live verification on a real Windows PC), Docker
   fallback (implemented, pending live test). Later: sandbox-exec on macOS.
3. **CI** - GitHub Actions: build, vet, run tests on every push.
4. **Unit tests** - model client, WhatsApp webhook, agent loop.
5. **Release v0.0.1** - tag, changelog, prebuilt binaries for Windows/Linux/macOS.

## Phase 2 - v0.2: memory and safety

6. **Long-term memory** - pgvector embeddings, recall beyond the last 20 messages.
7. **Secrets at rest** - AES-256-GCM encryption for stored credentials.
8. **Prompt-injection tests** - external content is data, never instructions; CI proves it.

## Phase 3 - v0.3: tools

9. **Web search** - pluggable backend, with a self-hosted SearXNG option.
10. **Browser** - drive the Playwright sidecar (already in docker compose).
11. **Email + calendar** - read and act on the user's accounts (OAuth).

## Phase 4 - v0.4: more channels

12. **iMessage** - bridge docs + reference implementation (needs a Mac).
13. **WhatsApp extras** - templates, media, groups.

## Phase 5 - setup that does not need a manual

14. **WhatsApp setup wizard** - a `fiveagent setup whatsapp` command that
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
