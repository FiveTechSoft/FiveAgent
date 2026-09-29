# Changelog

All notable changes to FiveAgent. Rule of this file: nothing is claimed
before it is implemented and verified in CI, and anything shipped but not
yet exercised against the real external service says so explicitly.

## v0.0.1 (release candidate - tag NOT cut yet)

Consolidation of roadmap stages 1-36. The v0.0.1 tag is intentionally
pending: it waits for battery run 6 (the baseline run against HEAD on the
dedicated runner) with the agreed gate of zero real hallucinations. When
that run passes, the tag is cut and prebuilt binaries are published.

### Highlights

- Self-hosted personal agent: one Go binary, `docker compose up` brings
  up the agent, Postgres and a Playwright sidecar. Model-agnostic (any
  OpenAI-compatible endpoint).
- WhatsApp channel via the official Cloud API, verified end-to-end on a
  live install; Telegram channel via long polling.
- 24 Go packages. CI builds, vets and tests on every push on Ubuntu,
  Windows and macOS, plus a Docker end-to-end job. The evals battery
  holds 20 scripted scenario suites (multi-turn conversations, failure
  injection, recovery, delivery, dedup, recall past truncation).

### Agent core

- Tool calling through the OpenAI function-calling protocol.
- Per-task model routing (first version): optional model override per
  request kind.
- Tool-call repair and repetition guard: malformed calls get one
  corrected retry; repeated identical calls are refused.
- Error-recovery classifier: one classified retry, then an honest
  failure narration - the battery's M7 metric scores a mismatch between
  the narrated error and the observed one as a hallucination.
- Context pruning, proven on a 61-turn scripted conversation.
- Durable delivery ledger: every reply is recorded before sending, so a
  crash mid-send never silently loses it; pending replies are
  redelivered once with a visible recovered marker, attempts capped at
  5, replies older than 24 h expire.
- Trajectory logging (opt-in): one JSONL trajectory per battery case
  with bounded rotation and redaction, designed as the future
  fine-tuning dataset source.

### Memory and secrets

- File-based long-term memory: plain markdown files with git as the
  source of truth. save_memory/forget_memory tools, per-turn recall
  injected as data (never instructions), keyword and alias retrieval,
  session digests, episodic learnings. CI evals cover recall past the
  20-message truncation window, corrections, dedup and injection cost.
- Memory effectiveness metrics M1-M7. The 2026-09-28 baseline caught
  4/7 memory setups that claimed to store a fact but never reached
  disk (M1 write-through); M3 (forgetting), M6 (anti-hallucination
  abstention) and M7 (error-narration fidelity) are measured.
- Secrets encrypted at rest: AES-256-GCM, key from env or a 0600 key
  file; plaintext stores migrate without loss.

### Channels and user-facing tools

- Links, not walls of text: long answers go out as signed, PIN-protected
  report links; secrets are collected with a small form link whose value
  lands in the vault, never in chat or logs.
- Cron scheduler: reminders and recurring automations in natural
  language, delivered back to the chat when they fire, auditable in
  data/jobs.json.
- WhatsApp status reactions (👀 / ✅ / ❌) and a media pipeline
  (inbound download by media id, outbound voice/image upload and send;
  partially implemented - live media services pending).
- Web search tool (CI-tested against fake servers; live endpoint
  verification pending).
- Workspace file tools with true diffs, scoped per user, with a go-git
  snapshot before every write so every edit is undoable.
- Sandboxed `run_command`: bubblewrap on Linux (CI-verified: no network,
  only the user's folder writable), AppContainer + Job Objects on
  Windows, Docker fallback. Backends preflight host viability and skip
  with the reason logged (wrong-arch image, missing privileges) instead
  of failing or weakening the fixture.
- Browser tools (`browse_page` / `browser_act`) with every form submit
  gated behind explicit user confirmation and a per-user audit log with
  passwords redacted.
- Integrations behind one shared OAuth pattern: Gmail, Google Calendar,
  Drive, Slack, GitHub - read and write tools, tokens at 0600 with
  transparent refresh (live OAuth verification pending).
- `send_chart`: bar/line charts rendered in pure Go, sent as native
  chat images; send failures surface as errors, never silent success.

### Multi-agent

- Subordinate agents: isolated subturns with fresh context.
- Parallel subtask execution: sibling subturns run concurrently.
- Unified conversation: one conversation layer across channels.
- Proactivity layer: event watches wake the agent instead of polling
  (live source watches pending).

### Extensibility

- `skills/` folder: domain knowledge files loaded on keyword trigger.
- MCP server: local, token-authenticated endpoint so an MCP client can
  run sandboxed commands and read workspace files; client config and
  smoke test in docs/mcp.md (live check from a real MCP client pending).

### Pending live verification

Shipped and CI-tested, not yet exercised against the real service:

- AppContainer sandbox on a real Windows PC.
- `web_search` against the live endpoint.
- OAuth flows for Gmail, Calendar, Drive, Slack, GitHub.
- WhatsApp live media services.
- Proactivity watches against real integration sources.
- MCP endpoint from a real client (procedure ready in docs/mcp.md).

### The v0.0.1 gate

Battery run 6 (baseline) runs against the tagged HEAD on the dedicated
runner with a gate of zero real hallucinations. Only then is the tag
cut.
