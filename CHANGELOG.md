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

- Explicit confirmation for effectful tools in the main turn (the app
  enables it; the library default is off). A fixed list of eleven tools
  (gmail_send, slack_send, calendar_create, github_create_issue,
  drive_upload, send_chart, run_command, browser_act, write_file,
  edit_file, schedule_job) does not run on first call: the model is told
  to describe the action and ask. The identical call runs only after the
  same sender's next message is a plain yes, once, within ten minutes;
  any other message clears the pending approval and another sender cannot
  approve it. Tools outside the list are unchanged and a new tool is not
  on the list until someone adds it. Tests with fake tools cover hold,
  yes, spent approval, other sender, unrelated message and default-off;
  disabling the gate makes them fail. Limits: scheduled jobs that call an
  effectful tool are held until the user answers, the yes match is a
  fixed set of short words, and it was not exercised with real connected
  accounts.

- Skill tool gating now applies at execution, not only to model specs.
  Each turn builds its own registry; helpers inherit that scope and keep
  their query-only allowlist. A shared tool is enabled by any active owning
  skill. Synthetic tests cover inactive and active skills in the main turn
  and sequential/parallel helpers, plus request scope propagation. Switching
  dispatch back to the full registry makes the rejection test fail. This is
  not a user-consent gate and was not exercised with real connected accounts.

- Helper tool dispatch is query-only: sequential and parallel helpers use
  the same fixed allowlist for model specs, argument repair and execution.
  Sends, writes, commands, browser actions and unknown tools stay blocked
  even when the model emits an unadvertised call. Synthetic mocks cover
  25 blocked tool names and 13 allowed queries, including parallel request
  scope propagation; removing the filter makes the rejection test fail.
  Main-turn tools are unchanged. This does not add a main-turn consent gate
  and does not prove safety with real connected accounts.

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
- `olvida:` no longer deletes a fact that only shares a bundled
  session digest with the forgotten one. Run 13's trajectory replay
  showed the mechanism: "olvida: mi plato de fiesta" (13:08:48)
  harvested seeds from a compacted digest line carrying both the
  plato and the food fact, the seed rule then erased the unrelated
  lacón preference, and the tools food-final turn (13:13:38)
  recalled digests only - the deferred MISS was a fact deleted five
  minutes earlier, not a model failure (the same story in runs
  11-12). Fix: a word carried by a curated entry the query does not
  match names another surviving fact and can never become a seed,
  in the scope that holds it and across scopes. Paraphrased digest
  copies still die on their curated original's seeds (run 7/9
  guard green). Red test first: TestForgetPlatoSparesFoodFact.
- Battery run 14: attempt 1 was invalidated by infrastructure (Ollama
  wedged - every generate timed out; server restarted), attempt 2
  scored 98 pass + 2 correct abstentions + 1 miss + 1 hallucination of
  101 (both parses agree). The food-final case PASSES for the first
  time (the seed fix above, verified live). The gate now fails on the
  post-forget ask: disk was clean (`recall=0`) yet the model cited
  `lacón` - it read the fact from the stage-7k frozen session snapshot,
  which `olvida:` never purges (only history messages get scrubbed).
  Run 13 passed that same case by model luck; the flap is stochastic
  on top of that real gap. Next round: drop or re-freeze the snapshot
  on `olvida:`/forget. AUDIT-MISSING (model wraps the missing command
  in `cmd /c` itself) persists unchanged.
- `olvida:` and `forget_memory` now invalidate the stage-7k frozen
  session snapshot: the system prompt no longer serves a fact the
  command just removed (the recall note cannot un-say a line the
  frozen block carries). Saving still never re-freezes - new facts
  keep arriving through recall, so the cacheable prefix only changes
  on a forget. Red test first: TestOlvidaPurgesFrozenSnapshot (turn 4
  after `olvida:` no longer contains the fact; the pre-forget turns
  still do).
- Exec scorer v2 (report only, label `exec-scorer-v2`): a saved
  execution run of another model scored v1 PASS on "echo FooBAR-Baz_123"
  although every run_command call had exited 127 (the model passed
  whole command lines as the program name, so no executable existed)
  and the reply was the token copied from the prompt, and PASS on the
  Windows launch question with `command="cmd /c"` + `args ["-c", ...]`.
  v1 checks words in the reply, not that they came from an observed
  execution. v2 adds verdict lines (`SCORER-V2 ...`) next to v1 for
  cases tagged `v2_exec_token` (needs an exit=0 audit line carrying the
  token; flags ENV-INVALID only when every failing call names the sandbox
  itself: daemon unreachable, image or arch mismatch; a missing
  executable or a runc line with its cause cut off stays a model miss)
  and `v2_argv` (exact command and first argument). v1 counts, the
  zero-hallucination gate and every historical run are unchanged; earlier
  runs are NOT re-scored (their raw logs are not in the repo). Fixtures
  in evals/scorer_v2_test.go replay the saved evidence and fail if the
  mechanism stops firing (checked by mutation). Not measured: v2 over a
  full live battery.
- run_command call shape: a whole command line in `command` (`sh -c
  "echo x"`, `echo 'x'`) or the shell glued to its flag (`cmd /c` with
  the payload in args) never ran under the no-shell rule; both shapes
  were measured in a saved run of another model. Now `sh -c "..."`,
  `bash -c`, `cmd /c "..."` fold into the intended argv (flag first, the
  payload one argument, logged as a normalized call); any other
  whitespace-bearing command with no args is rejected unrun with the
  exact corrected call in the error; a payload in both command and args
  is rejected as ambiguous; paths with spaces are untouched. Tests
  check each shape and fail when either mechanism is disabled (checked
  by mutation). Not measured: effect on a live battery run.
- Scorer v2 now also tags the auditoria-cinco case and prints one
  `METRIC scorer-v2` summary line per run; README says how to compare v1
  and v2 over saved logs.
- Battery run 15 (first run with the snapshot purge): 97 pass + 2
  correct abstentions + 3 misses + 0 hallucinations of 102 (both
  parses agree) - GATE MET. The run-14b failure is gone: the
  post-forget ask no longer quotes the fact (disk and system prompt
  both clean). Remaining violations are stochastic and unrelated to
  memory: a logic riddle answered with alternatives instead of the
  expected word, a post-forget answer that hedged instead of a clean
  abstention, and the known AUDIT-MISSING (`cmd /c` wrapper chosen
  by the model itself) - its harness expectation awaits a decision.
- Decision on that AUDIT-MISSING: the missing-command prompt now
  scores with `audit_token: comando_que_no_existe_xyz123` instead of
  `audit_contains: error=sandbox`. Both failure shapes are legitimate
  - bare CreateProcess `error=sandbox` (runs 10-12) and `cmd /c`
  exit=1 + "no se reconoce" (runs 13-15), the wrapped one being the
  shape the battery's own `v2_argv: cmd|/c` documents for Windows -
  so the check now demands the target command AND a failed execution
  in the SAME run_command audit line. That also rejects an exit=0
  echo of the target, an unrelated failing command, and an agent
  line that only names the target (all false passes a token-only
  match would allow). Red test first: TestAuditMiss replays the
  verbatim audit lines of runs 10-15. First measured by battery run
  16, next bullet.
- Battery run 16 (first live run with `audit_token`, 90 min, tree
  fb07524): 96 pass + 4 correct abstentions + 2 misses + 0
  hallucinations of 102 scored (113 prompts, 20 categories; python
  scorecard and PS regex agree) - GATE MET. The fixed prompt passed
  on the very shape that failed runs 13-15: the model again wrapped
  the missing command in `cmd /c` (audit `exit=1` + "no se
  reconoce"), `audit_token` matched it, tools reads 7 pass + 1
  abstention + 0 miss, and the log holds zero AUDIT-MISSING lines.
  Scorer v2: exec ok=2, exec env=0, exec fail=0, argv ok=1, argv
  fail=0. Neither miss touches memory: the compra.txt files case
  (`sh -c` fails CreateProcess - no sh.exe in the Windows sandbox -
  and the Windows Store `python3` alias then answered exit=0 with
  "Failed to find real location" on stderr, so the model hedged into
  an abstention), and the weekday ask, where the model called
  `current_datetime` without a timezone - which defaults to UTC
  (internal/tools/datetime.go) - and answered domingo/23:46 UTC
  inside the 00:00-01:59 Madrid window where the rubric's
  `{{weekday}}` resolves to lunes: a tool-default vs rubric
  timezone boundary, deterministic for late-night runs.
- The run_command rejection hint now recommends the OS shell instead
  of always `sh -c`: battery run 16 shows the model receiving that
  hint on three rejections in a row and following it once into a
  CreateProcess failure (no sh.exe on Windows) while the tool
  description already taught `cmd /c` - hint and description
  disagreed. `shellExample(goos)` is the single source for both;
  red test first: TestRunCommandRejectionHintsTheOSShell fails on
  Windows when the hint says sh.
- current_datetime without a timezone argument no longer answers in UTC:
  it uses the new optional `timezone:` config key (IANA name, invalid
  names stop startup) and otherwise the host's local zone, the same one
  cron and the calendar already read. Why: the CHANGELOG entry of
  battery run 16 (OpenCode's text only, not verified by us) reports the
  model naming domingo at 23:46 UTC when it was already lunes in Madrid;
  the UTC default in the code is verified, and the tests reproduce that
  window (23:46 UTC on a Sunday is 01:46 Monday at UTC+2). Tests pin the
  host zone so they cannot pass by CI-host luck, and fail when either
  the configured zone or the host fallback is bypassed (checked by
  mutation). An explicit timezone argument still wins. Not measured: a
  live battery run with this change; a Linux container with no TZ and no
  `timezone:` still reads as UTC.

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
