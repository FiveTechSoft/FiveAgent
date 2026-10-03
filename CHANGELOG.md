# Changelog

All notable changes to FiveAgent. Rule of this file: nothing is claimed
before it is implemented and verified in CI, and anything shipped but not
yet exercised against the real external service says so explicitly.

## v0.0.1 (2026-10-02)

Consolidation of roadmap stages 1-36, first tagged release. Battery
result reported for this tree: live run 11 (113 prompts, 20 categories,
102 scored): 96 pass + 2 correct abstentions + 4 misses, 0 hallucinations
flagged by the battery detector, M1 write-through 8/8, M3 effective
forgetting 1/1. Read this number with three caveats:

- The detector was adjusted after seeing runs 6 and 7 (a translated
  "access denied" no longer counts as invented, "avoid X" phrases are
  not read as recommending X, three more abstention phrasings count).
  Each change has a unit test, but it is not the unmodified rubric. The
  unmodified rubric on the earlier full run against 6bc4863 scored
  97/102 with 1 hallucination flagged.
- The raw output of run 11 (scorecard, trajectories) is not stored in
  the repository, so the figures above cannot be re-checked from it.
- Real command execution in the sandbox was verified in only 2 cases.

Prebuilt binaries for Windows, Linux and macOS are attached to the
GitHub release. The 4 misses are metrics, not gates: two are recall
calls the model skipped, one is the sandbox denying `bash -c` (the
prompt's own error path), one is an ambiguity the abstention detector
could not count.

### Added after consolidation

- `fiveagent setup whatsapp`: checks the access token and phone number
  id, optionally subscribes the app to the WhatsApp Business Account
  (with read-back) and registers the webhook callback. Covered by tests
  against a fake Graph server; not yet exercised against the real Meta
  API.
- Memory aging (stage 7l): `archive_days` rides the idle
  consolidation pass - stamped entries older than N days move to
  `archive/<file>.md`, a subdirectory recall, the FTS index and the
  snapshot never walk, so an aged fact stops resurfacing while staying
  on disk and in git history. Unstamped lines never age, and `olvida:`
  still reaches archived copies through the same forget path. Default
  0 (disabled); unit-tested red-to-green in internal/memory and
  internal/agent.
- Prompt-injection defenses (stage 9): every system prompt now marks
  command output, web results, file contents, recalled memories,
  subordinate replies and quoted third-party text as untrusted data,
  and a triggered skill block carries a header scoping it to its own
  task. Adversarial CI tests push an "ignore all previous
  instructions" payload through a tool result and a stored memory note
  and assert it only rides as labeled data on a byte-stable system
  prompt. The inbound-message vector ships with the multi-user opening.
- `olvida:` and `forget_memory` are effective on disk (M3): purge
  tokens derived from the stored fact reach every copy of it - curated
  files, session digests (paraphrases included) and per-user scopes -
  and the forgotten words are redacted from stored session history, so
  the model cannot quote the fact back from old turns. Battery runs
  10 and 11 both report memory-erase 1/1 (raw output not stored in the
  repository).
- Memory notes carry a date and an origin: Append writes a stamp
  (`[date, origin: ...]`) on every new note; duplicates are still
  detected across days and origins. Unit-tested. Retrieval of an absent
  fact returning nothing (not a look-alike) is tested at the memory
  layer only; how the model words "not found" is not measured.
- `run_command` runs Windows shell builtins (the run 11 miss described
  above as "the sandbox denying bash -c" was in fact a bare `echo`):
  the model invoked `echo` exactly as the prompt asked, but Windows has
  no echo.exe, so CreateProcess reported "cannot find the file". The
  tool now retries a missing-file failure once through `cmd /c` when
  the command is a bare cmd builtin (echo, dir, copy, ...). Unknown
  executables and other errors keep their original error unchanged, so
  the battery's not-found narration cases still see the same text.
  Unit-tested red-to-green (retry fires for builtins only) and verified
  live against the real appcontainer backend on Windows.
- Battery run 12 (2026-10-03, tree = run 11 + the cmd-builtin fix):
  96 pass + 3 correct abstentions + 2 misses + 1 hallucination of 102
  scored (both scorecard parses agree). The builtin retry flips the
  FooBAR/echo case to pass; the M2c restart case recovers; the two
  food-memory cases persist - the deferred reply cited other
  remembered facts but not the target one. The zero-hallucination
  gate failed on one stochastic harbour trap (`cVar`) that run 11
  passed: the gate is not deterministic run-to-run. Every turn now
  logs   `memory injection: snapshot=N chars, recall=N lines from
  [...]`, so the next run says whether the fact was injected at all.
- Battery run 13 (2026-10-03, first run with the injection log):
  95 pass + 4 correct abstentions + 3 misses + 0 hallucinations of
  102 scored (both scorecard parses agree) - GATE MET. The log
  adjudicates the deferred food miss: recall returned 6 lines, all
  from digests, none from preferences - the fact was never injected,
  so that miss is recall-side, not model neglect. Two local
  diagnostics (isolated store; plus digests noise) both pass, so the
  drop needs the full battery fixture; next round replays it. The
  model itself wrapped the nonexistent command in `cmd /c`, so the
  error was cmd's "not recognized" instead of sandbox CreateProcess
  and `audit_contains: error=sandbox` missed (zero occurrences in
  runs 11-12 - a new stochastic class, not a regression: the
  builtin retry only fires for bare builtins and still passed the
  echo case this run).

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
- Memory stage 7c: recall runs on an embedded SQLite FTS5 index
  (modernc.org/sqlite, pure Go, cgo-free releases) as a rebuildable
  cache over the markdown files - the files stay the source of truth,
  the index rebuilds on drift and on every write, and any index
  failure falls back to the keyword path.
- Memory stages 7k and 7l: a session starts with a frozen memory
  snapshot in the system prompt (byte-stable prefix; mid-session
  writes reach the model through recall, never a prompt rewrite - the
  M5 gate is CI-proved), and idle-time consolidation merges
  near-duplicate facts in the background after
  `memory.consolidate_idle_minutes` of no turns (rules slice; aging,
  re-filing and the model pass stay pending).
- Battery run-6 packaging (stages 24a, 25e, 7m): the live runner
  mints its run report through the make_report_link machinery
  (MintReportHTML: signed link + PIN in the run log), the report's
  pass-rate chart is the send_chart renderer's output embedded in the
  page, and the runner supports the M2 deferred (pad_turns) and
  restart (restart_before) recall depths plus the M2c post-restart
  resurrection check and M3 on-disk erase metric. Battery grows to
  113 prompts.
- Per-request-kind sampling (stage 11a): `sampling.tool_temperature`
  cools tool-calling rounds, `sampling.chat_temperature` warms the
  final-answer retry, `sampling.presence_penalty` fights repetition
  loops. Unset fields are not sent (provider defaults apply); values
  outside the provider range fail config validation. On Ollama's native
  route they ride the options map next to num_thread.
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
- `num_thread` per model: caps Ollama's inference threads so the runner
  stays a good neighbor on shared CPU hosts. Propagated through Ollama's
  native /api/chat (the OpenAI-compatible layer silently drops unknown
  options); a base_url where it cannot be honored fails loudly at load.
- `chat_template_kwargs` per model: arbitrary chat-template switches in
  the /v1/chat/completions body (measured need: enable_thinking: false
  stops a reasoning model on SGLang from spending the whole token
  budget on internal thinking and replying empty). Separate route from
  num_thread; setting both on one model fails loudly at load.

### Memory and secrets

- File-based long-term memory: plain markdown files with git as the
  source of truth. save_memory/forget_memory/save_learning tools,
  per-turn recall injected as data (never instructions), keyword and
  alias retrieval. Session digests: when context pruning compacts the
  middle of a long conversation, the summary lands in the sender's own
  digests.md and stays recallable. Episodic learnings: the agent
  records short self-critiques through save_learning, and a 👎/👍/❤️
  reaction on a reply is recorded as feedback on that exact reply -
  both in the sender's own scope. CI evals cover recall past the
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

The agreed gate was zero real hallucinations on a battery run against
the tagged tree. Run 11 reports 0 flagged by the adjusted detector; the
unmodified rubric flagged 1 on the earlier full run. The tag was cut on
that basis. Whether the gate counts as met with an adjusted detector is
an open decision, and the raw run output still has to be added to the
repository so the claim can be checked.
