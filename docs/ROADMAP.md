# FiveAgent roadmap

Ordered, one thing at a time, simple first. Each item is a GitHub issue;
the phases are milestones.

One rule governs everything below: nothing counts as working until it
has been measured. Every stage ships with its own battery of
benchmarks and is "done" only when those benchmarks pass - never on
intention, never on vibes. A claim without a run, a test or a
benchmark behind it is marked "pending live verification" or stays
out of the docs. When something changes (a model, a tool, a prompt),
the same battery re-runs and the before/after comparison is the only
evidence that counts.

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
   a. **Minimal memory** (working) - three files: people.md,
      preferences.md, workstreams.md, with a git commit on every write.
      The model curates them through save_memory / forget_memory tools
      (duplicates are skipped) and every turn recalls from the files:
      memories are injected labeled as data, never instructions, and
      never depend on the conversation history surviving truncation.
      Empty folders from day one are debt; the structure grows only
      when real use demands it.
   b. **Keyword and alias retrieval** (working) - recall by exact words
      and curated aliases kept in each memory file's header.
   c. **SQLite FTS5 index** - an embedded full-text index as a
      rebuildable cache. The files stay the source of truth; the index
      is disposable. Database speed, zero infrastructure.
   d. **Organic growth and links** - new files and folders appear when
      they hurt; [[id]] links connect related records (a plain-text
      graph, parsed when needed). A graph or vector database only
      enters if real scale one day demands it.
   e. **Consolidation** - merge near-duplicate facts and age out stale
      ones; starts as rules plus a summary pass with the model itself.
   f. **Episodic memory** - a learnings.md where the agent writes a
      short self-critique in plain words when a task fails or the user
      corrects it ("no, the other Taylor"), recalled as data in later
      turns. User corrections are the evaluation signal, and in a chat
      bot they come for free. Inbound emoji reactions are free feedback
      too: keep the wamid-to-reply mapping at send time, record 👍/❤️
      as positive and 👎 as negative feedback on that exact reply, and
      feed it to learnings and eval scenarios. (Inbound reactions are
      already intercepted before the agent: they never trigger a
      turn.)
   g. **Rolling session digest** - today the conversation history
      truncates to the last 20 messages, so old detail is lost unless
      the agent stored it. Before truncating, summarize the outgoing
      messages into a session digest file, also recalled as data in
      later turns.
   h. **Memory evals** (working in CI) - evals/ runs scripted
      conversations in CI: a fact stored on turn 1 is recalled on turn
      32 (past truncation), corrections remove the old value, dedup
      rejects repeats, injection precision and per-turn injected cost
      are measured. Model-judgment evals (does the model save and use
      memories well) run locally against the real model
      (FIVEAGENT_EVAL_LIVE=1) and are the gate for marking a stage
      done. First catch: the precision eval proved template description
      lines polluted recall; recall now scores fact bullets only.
      Second catch: the scaling eval showed injection cost growing
      without bound on popular words; recall now caps each file at its
      10 most recent matching bullets.
   i. **Per-sender memory scoping** (decision pending) - knowledge is
      global across senders today, while conversation history is
      already per sender. For a single-owner personal bot that is
      fine; if the bot ever serves more than one person, one sender's
      memories would be visible to the rest. Decide the scoping model
      (per-sender files vs one shared owner memory) before opening the
      bot to multiple users.
   j. **Memory privacy** - people.md and preferences.md will hold
      personal data about real people. Decide early: who can read
      data/memory, encryption at rest, and a full-person wipe path
      ("forget everything about me") - forget_memory removes single
      notes, but erasing a person entirely deserves its own flow.
   k. **Frozen memory snapshot** (planned) - today recall runs per
      turn (keyword/alias/FTS queries against the files). Add the
      complementary path: at session start, inject the memory files
      (or their digest) as a FROZEN snapshot in the system prompt -
      mid-session writes hit disk but do not rewrite the prompt, so
      the prefix stays byte-stable and cacheable, and the model keeps
      a coherent "who is this person" picture without a recall hit.
      Done when: a session starts with the snapshot injected, a
      save_memory mid-session is recalled by query (not by prompt
      rewrite), and the prefix hash of the system prompt is unchanged
      after the write; the battery grows a "snapshot" case verifying
      both.
   l. **Idle-time consolidation** (planned) - when the agent is idle,
      a background pass consolidates memory: merge near-duplicate
      facts, re-file misplaced ones, age out stale entries, and
      refresh the session digests. Zero latency cost in the user's
      turn; the files stay the source of truth.
      Done when: after a scripted day of conversations, an idle pass
      merges the seeded duplicates and the next recall returns the
      merged fact once; the battery grows an "idle-consolidation"
      case verifying the merge and that no fact was lost.
   m. **Memory effectiveness metrics** (designed 2026-09-28, partially
      implemented) - memory quality as numbers, not anecdotes. Six
      metrics: M1 write-through rate (every recuerda: setup must leave
      the fact in the memory files on disk, checked by the battery's
      memory_writes field; the 2026-09-28 baseline showed 4/7 setups
      never reached disk even when the reply claimed they did -
      implemented), M2 recall at three depths (immediate, deferred past
      the 20-message history window, and after a full session restart;
      the deferred and restart depths need runner support), M3
      effective forgetting (after olvida:: honest abstention -
      implemented - plus the fact gone from disk and never resurfacing
      in open questions like "what do you know about me?"; 2026-09-29
      refinement: a model quoting the forgotten fact from the
      still-visible session context is a context citation, not a
      resurrection - the true resurrection check is the post-restart
      M2c depth, pending runner support), M4
      cross-user non-contamination (one sender's facts never surface
      for another; defined but DISABLED until 7i decides the scoping
      model - we do not measure what the design does not yet require),
      M5 frozen-snapshot stability (a mid-session write must not
      rewrite the system prompt prefix; the hard gate of 7k), M6
      memory anti-hallucination (a never-stored fact yields
      abstention, not invention). Evolution is measured, not told:
      every live run dumps its metrics to a JSON artifact under
      evals/out/ (stage 12's trajectory logger becomes their natural
      transport), a historical scorecard in evals/README.md tracks the
      numbers run over run, and a memory metric that drops versus the
      last scorecard with the same model blocks the "done" of the
      stage that caused it until the drop is explained. Hallucinations
      stay the hard gate of every run.
8. **Secrets at rest** - AES-256-GCM encryption for stored credentials.
9. **Prompt-injection tests** - external content is data, never instructions; CI proves it.
   Today this covers memory content; before the bot opens to multiple
   users it must also cover the main vector: the inbound messages
   themselves ("ignore your instructions and..."). Hardened system
   rules plus adversarial evals ship with any multi-user opening.
10. **Per-task model routing** (first version working) - an optional
   `coder:` model in fiveagent.yml serves code-heavy requests. Each
   message goes through a local heuristic (two tiers of signals with
   word boundaries, no model call): code goes to the coder model,
   everything else to the chat model. The coder inherits base_url,
   provider and api_key from model when omitted. Instruction shape is
   itself a routing signal: compound requests (look up X and apply Y,
   do A then B, answer under a negated constraint) are where small
   models silently drop clauses, so detecting them should route to
   the larger model - the battery's instrucciones_compuestas category
   measures exactly this failure. The 2026-09-28 live run 3 added a
   second shape: fuzzy riddles and lateral-logic prompts (the 9B
   answers montana/piano where the rubric wants edad/teclado) - same
   treatment. Later: more task classes beyond chat/code and fully
   configurable model sets.
   a. **Hybrid cloud escalation** (decision module implemented and
      CI-tested; the cloud provider itself is still planned) - top
      priority, core security piece: it is the single point that
      decides whether a turn may leave the machine. Local by default.
      The harness decides, never the model: escalation fires only on
      measured evidence of local failure - an abstention, a tripped
      repetition guard, a repair storm (>= 3 rescued tool calls in one
      turn), or a failed response verification. The model can raise
      its hand (self-declared uncertainty), but that weak signal alone
      never escalates. Opt-in by design (off by default): with
      escalation disabled the decision provably never fires - the
      tests enumerate the entire decision table (every combination of
      signals x opt-in) - and no code path to an external provider
      exists: internal/agent/escalation.go is a pure deterministic
      function with zero imports and zero I/O, guarded by a test that
      fails if it ever gains one. Without the opt-in the answer is
      the honest local one. Done when: (decision layer: done) the
      seven claims hold in CI - clean turn stays local, abstention /
      guard / repair storm / failed verification escalate, model
      uncertainty alone does not, opt-in off never escalates -
      and (provider, later) with escalation enabled an over-local
      request reaches the provider and its reply is delivered, with
      the escalation logged like any other turn.
   b. **Commercial-to-local learning loop** (planned, high priority,
      core) - every escalated, opted-in commercial answer is captured
      through the trajectory logger (12) and distilled into verified
      few-shot examples kept in a per-domain library, injected when
      the topic matches. No fine-tuning: the weights stay untouched,
      the harness gets smarter. Done when: measured before/after on
      the battery - prompts the local model used to fail pass after
      their example enters the library, and examples that do not move
      the number are removed.
11. **Model tuning** (planned) - get the most out of the local model,
   each step adopted or dropped by evals, never vibes, roughly in
   cost/benefit order:
   a. **Sampling parameters** - low temperature for tool calls and
      facts, higher for chat; presence_penalty against the repetition
      loops small models fall into.
   b. **Structured outputs** - force a JSON schema on tool calls
      (Ollama supports it): turns "almost always parses" into
      "always parses" for a small model.
   c. **Few-shot system prompt** - 2-3 examples of perfect
      interactions; small models punch far above their weight with
      concrete examples.
   d. **Tool discipline** - few tools, well described; every extra
      tool degrades a small model.
   e. **Explicit planning step** - for complex tasks, plan before
      acting; small models fail by skipping steps, not by capacity.
   f. **Quantization choices** - a 9b at q8 usually beats a 14b at q4
      at equal VRAM. Measurable.
   g. **LoRA on real conversations** (long-term) - the end of the
      curve, not the start; a 9b adapter fits in 12GB with Unsloth.
   h. **Teacher-model distillation** - a big teacher (e.g. DeepSeek)
      lifts the small model two ways. Artifacts: the teacher writes
      the perfect few-shots, eval scenarios and flawless tool-call
      traces that the small model consumes in prompts and tests every
      turn. Real distillation: the teacher generates training data
      (conversations, corrections, good tool calls), the evals filter
      it - a big teacher hallucinates too - and it feeds the LoRA of
      stage g. The teacher is a data factory, the small model is the
      distillate, the evals are quality control.
   First candidates: a and b (one afternoon, direct impact), then c.

## Phase 3 - v0.3: tools

12. **Trajectory logging** (planned) - record every battery run (and
    later, opt-in, real sessions) as JSONL trajectories: the turns
    (user/assistant/tool, with the tool calls and their results), plus
    per-tool usage stats (count/success/failure). A run of the battery
    becomes a durable, comparable artifact: what the model did, not
    just pass/fail. The data accrues value with every run.
    Done when: a battery run writes one JSONL trajectory per case plus
    a tool-stats summary, and a report compares two runs by their
    artifacts; the battery grows a "trajectory" case verifying the
    log's schema and the stats.
13. **Fine-tuning dataset** (future) - turn the battery into a
    training-data factory: run the same battery with a stronger
    model, keep only the trajectories that PASS the rubric, and use
    them to fine-tune the local model. The battery is both the data
    generator and the quality gate - only behavior that survives our
    honesty and tool-use checks enters the dataset. Needs GPU and
    tuning tooling; the trajectory logger (stage 12) is the
    foundation.
    Done when: a dataset of rubric-passing trajectories exists and a
    fine-tuned local model scores measurably higher on the battery
    than its base model. The verdict is a before/after benchmark on
    the SAME battery, with the SAME rubric and the same live
    conditions - the battery itself is the judge, and a fine-tune
    that does not move the numbers is reverted, not explained.
14. **Tool-call repair and repetition guard** (implemented, CI-tested;
    live-rescue counts accrue in real traffic) - small
    models emit almost-right tool calls: "42" as a string where an
    int goes, "true" as a string, a JSON blob where an array goes, a
    scalar where a list goes. Repair them before dispatch: a
    conservative, schema-guided coercion that only applies
    unambiguous fixes (anything doubtful goes back to the model as an
    error). Plus a repetition guard: when a reply is dominated by one
    long repeated fragment, abort the turn with a clear error instead
    of delivering the echo.
    Done when: the battery grows cases with mistyped tool arguments
    that succeed after coercion and a degenerate repetition that is
    caught before delivery; the run report counts rescued calls.
15. **Context pruning** (implemented, CI-tested) - the context
    window is the small model's scarcest resource, and raw tool
    outputs are what floods it (one long directory listing costs more
    than a day of chat). internal/agent/prune.go prunes the history in
    order: first old tool outputs outside the tail are truncated to a
    keep-prefix, then the middle turns are compacted into one message
    - a summary from an auxiliary model pass (the chat model itself,
    one extra call only when over budget), falling back to an explicit
    "N earlier turns omitted" marker when no summarizer is available
    or it fails. The head (first exchange) and the recent tail always
    survive byte-identical; messages are grouped into blocks so a cut
    can never split a tool call from its result; the store keeps the
    full conversation (pruning rewrites only the in-memory copy). The
    old hard truncation (last 20 messages) is gone: the agent reads
    up to 200 and prunes to a 24k-char budget.
    Done when: (met) a 61-turn scripted conversation with verbose
    turns and tool calls stays inside budget and still recalls its
    turn-1 fact verbatim (evals/prune_test.go, CI); the memory metric
    "recall past truncation" now exercises a fact compacted out of the
    middle (evals/memory_test.go); 7 unit tests cover the ordering,
    the head/tail protection, the pair invariant, the summarizer
    fallback and the unprunable case. Scope note: pruning applies to
    the stored history; growth inside a single turn's tool rounds is
    bounded by maxToolRounds.
16. **Error recovery classifier** (implemented, CI-tested) - one
    pipeline maps every model-API failure to its recovery instead of
    string-matching errors inside the loop.
    internal/model/classify.go classifies each failure into a kind
    (timeout, rate-limit, auth, context overflow, malformed reply,
    unavailable) wrapped in model.Failure; internal/agent/recover.go
    runs the ladder per kind: transient kinds (timeout, rate-limit,
    malformed) retry with backoff, overflow prunes the context with
    the stage-15 pruner and retries once, an empty reply (200 with no
    content and no tool calls - a generation failure, not transport)
    retries with a reinforced prompt, then any retryable failure
    falls back to the other configured model; auth aborts honestly
    naming the credential; when both models fail the error names both
    kinds, and with no fallback configured the abort says so. A reply
    still empty after the whole ladder degrades to one fixed honest
    line - never silence, never a turn-level error.
    Scope note: the model client is non-streaming request/response
    today, so the adaptive non-streaming degradation for endpoints
    that answer streams with empty keepalive frames has nothing to
    attach to yet - it returns when streaming exists.
    Done when: (met) the failure-injection battery
    (evals/recover_test.go) forces each classified failure through a
    scripted endpoint and asserts its mapped recovery and the single
    clear outcome: rate-limit recovers on the third call, malformed
    JSON recovers on retry, overflow prunes and the retry is strictly
    smaller (an overflow must be followed by a shrink - without
    pruning the case fails), auth aborts on the first 401 naming the
    credential with no retry storm, a dead main model falls back to
    the healthy coder model, a permanently slow endpoint aborts
    naming the timeout, an empty reply recovers on a retry that must
    carry the reinforced prompt (asserted from the request bodies),
    and an always-empty model degrades to the fixed honest line in
    exactly 6 model calls. 14 classifier unit tests
    (internal/model/classify_test.go) pin the status/body/network
    mapping, the kind names and errors.As survival through wrapping.
17. **Keyword-triggered context** (planned, with Skills) - skill and
    domain instructions enter the context only when the message
    mentions their trigger words, never by default. Sibling of the
    memory recall-by-alias: the context only pays for what the turn
    needs.
    Done when: with several skills installed, an unrelated turn
    carries zero skill text and a matching turn carries exactly one;
    the battery grows a "trigger" case measuring injected tokens.
18. **Subordinate agents** (planned) - the agent splits a big task
    into small subtasks and runs each in an isolated sub-turn with
    its own fresh context, then composes the results. The single
    most effective harness technique for small models: no subtask
    exceeds what the model can do in one clean turn. Simple first:
    sequential subordinates, no parallelism (that is stage 34).
    Done when: a multi-step task that fails as a single turn succeeds
    decomposed; the battery grows a "subordinate" case comparing both.
19. **Durable delivery ledger** (planned) - every outbound reply is
    recorded as a persistent delivery obligation (pending ->
    attempting -> delivered) so a crash between generating and
    sending never loses or duplicates a reply; a redelivery after a
    crash carries a visible "recovered" marker. Attempts are capped
    and stale entries expire.
    Done when: a killed-mid-send test redelivers exactly once with
    the marker; the battery grows a "delivery" case over a crash
    fixture.
20. **Web search** (implemented, CI-tested against fake servers; pending
    first live run) - the `web_search` tool with pluggable providers:
    DuckDuckGo by default (no API key, may rate-limit under heavy use),
    Brave via `web_search.api_key` for production. `web_search:` section
    in the yml. A self-hosted SearXNG provider remains a welcome option.
21. **Workspace tools** (planned) - real filesystem tools for the
    agent: read_file, write_file and edit_file with true diffs (the
    model passes old/new text, the tool verifies the exact context and
    returns the applied diff), all scoped to the user's folder. Every
    write takes an automatic snapshot with go-git (already a
    dependency), so every edit is undoable and auditable - no more
    destructive shell redirects. Base for the later git tool.
    Done when: the agent creates, edits and fixes a file over WhatsApp
    and returns the exact diff applied; the battery grows a "files"
    case that verifies the edit and the rollback snapshot.
22. **Cron scheduler** (planned) - scheduled automations in natural
    language, delivered to any channel: daily reports, nightly
    backups, weekly audits, reminders. The scheduler lives in the
    agent process, fires jobs unattended, and delivers the result to
    the user's WhatsApp/Telegram. Each job is auditable (what ran,
    when, what it sent).
    Done when: the user asks "recuérdame X mañana a las 9" over
    WhatsApp and the reminder arrives at that time on the same
    channel; the battery grows a "cron" case that creates a job,
    fires it, and verifies the delivery text.
23. **Browser** (planned) - a headless Playwright browser as a native
    tool, OUTSIDE the command sandbox (which stays offline): one isolated
    browser profile per user, downloads land in the user's folder.
    Automation by DOM / accessibility tree, never pixels: the agent gets
    the page as a numbered list of elements and acts by id ("fill #3",
    "click #7") - small models fail at vision-coordinate clicking but
    handle structured DOM well. Safety by design: user confirmation
    before submitting forms that spend money or send data, credentials
    live in a local vault and never enter prompts or logs, optional
    per-skill domain allowlist, and every click/fill is audit-logged.
    Done when: the battery grows a "web" category that fills a local
    test form end-to-end, and a confirmation-gate eval proves a purchase
    form is never submitted without user approval.
24. **Links** (planned) - the bot answers with links served by its own
    HTTP server (the same listener as the webhook), not with
    wall-of-text messages:
    a) Reports: the bot generates a long report (battery results,
       doctor output, analysis) and replies with a link to a clean HTML
       page. First use case: the battery report with per-category
       metrics compared against stored baselines.
    b) Data collection: the bot sends a link to a small form for
       sensitive data (tokens, passwords); what the user types goes
       straight to the vault/config, never through WhatsApp messages
       and never into logs.
    Security by design: links are signed with a token and carry an
    expiry; they are only generated for allowed senders; the form
    handler never writes submitted values to logs; the server binds to
    localhost by default and external access requires an explicit
    tunnel.
    Done when: the battery grows a "links" category proving that
    (i) a report link renders the battery report and is rejected after
    expiry or with a wrong signature, (ii) a form submission lands in
    the vault/config and its value appears in no log line, (iii) a
    link minted for one sender is rejected when opened by another.
25. **Media** (planned) - WhatsApp media pipeline in BOTH directions:
    the webhook receives image/audio/video/document with a media id,
    downloads it with an authenticated Graph API call, dispatches by
    type, and the result enters the normal message flow; outbound, the
    bot synthesizes voice and images and sends them as native WhatsApp
    media (upload + send via Graph API).
    Inbound phases:
    a) Voice notes: opus audio -> local Whisper transcription
       (whisper.cpp / faster-whisper) -> treated as a text message.
    b) Images: download -> vision model (a VL variant configurable in
       the yml; document that text-only qwen models cannot see and a VL
       that fits in 12GB is needed) -> description into the context.
    c) Video (last): key frames + transcribed audio -> summary.
    Outbound phases:
    d) Voice: local TTS (Piper/Kokoro, Spanish) -> the reply goes out
       as a WhatsApp voice note; enabled per user on request
       ("respóndeme por voz").
    e) Images: first code-made artifacts - browser/sandbox screenshots,
       battery report charts, diagrams; AI image generation (local SD)
       is an optional final phase.
    Security by design: media is only fetched and processed from
    allowed senders; no media content (or transcript) lands in logs in
    the clear.
    Done when: the battery grows a "media" category that proves the
    round trip - a test image of known content and a voice note with a
    known phrase, both scored with must_contain on the agent's reply,
    plus an outbound case where the bot emits audio/image and the
    (fake) Graph server confirms the upload and the send.
26. **VM GUI** (planned) - a Linux VM with a lightweight desktop (XFCE
    or similar) on the server, QEMU/KVM, powered on demand, not 24/7.
    Real desktop screenshots via QEMU screendump or VNC, taken on
    demand and after each action, never continuous video; delivered
    over WhatsApp/Links (stage 14). The agent drives the desktop
    through the accessibility tree (AT-SPI): element ids, never
    pixels; a vision model is used ONLY to verify outcomes, loaded
    on demand (document the VRAM contention with the chat model).
    The VM is one more sandbox level: no network except an allowlist,
    clean snapshots.
    Done when: the agent opens an app in the VM, acts on it through
    the accessibility tree, and sends real before/after screenshots
    over WhatsApp; the battery grows a "vm" case that verifies both
    the screenshot and the action.
27. **Email + calendar** - read and act on the user's accounts (OAuth).

## Phase 4 - v0.4: more channels

28. **iMessage** - bridge docs + reference implementation (needs a Mac).
29. **WhatsApp extras** - status reactions (working): 👀 when an
    inbound message checks out, ✅ when the reply lands, ⚠️ on failure,
    through the same /messages endpoint, replacing the previous
    reaction on the same message, best-effort (a failed reaction never
    breaks the reply), behind `reactions: off|status` in the yml.
    Burst debounce (working): rapid messages from one sender join a
    single turn (3s window, `debounce` in the yml), one reply with the
    full context in arrival order, quoting the last message. Then
    templates, media, groups. Telegram reactions (setMessageReaction)
    follow the same pattern later.
30. **Content reactions** (planned) - on top of status reactions, the
    agent reacts to what a message says, not just its state: a
    celebration, a joke, a thank-you gets a fitting emoji chosen from
    the message content. Cheap to build (a small extra model call or
    simple rules on top of the existing React()); whether rules are
    enough or the model chooses is decided by evals, not taste.

## Phase 4b - v0.4b: growing the small model

32. **Skills** (planned) - a `skills/` folder with one SKILL.md per
    domain: name, one-line trigger, concise procedure written for a
    small model. Only the one-line index enters the system prompt; the
    full skill loads on demand when the request matches (keywords or a
    cheap classifier), with optional tools associated to the skill.
    docs/fivetech-domain.md migrates to this format as the first skill.
    This is how the small model "learns from the big ones": expertise is
    written down once instead of re-derived per session. Done when: the
    battery runs the same prompts with and without the matching skill
    and reports the delta per category, and the domain answers pass only
    with the skill loaded.
33. **FiveAgent as MCP server** (planned) - expose a local,
    token-authenticated MCP endpoint so an external agent (e.g. the
    owner's OpenCode) can execute commands inside the sandbox and read
    files from the user's folder. Documented in docs/. Done when: an
    integration test drives the endpoint with a fake client (auth
    rejected without token, command runs confined to the sandbox), and
    the doc page gets a reader from zero to first call in minutes.

34. **Multi-agent parallel execution** (future path, for large models;
    see the single-agent principle below) - a worker pool (goroutines)
    fed by a task queue: N concurrent tasks, each isolated in its own
    context, sharing nothing mutable. Memory stays thread-safe under
    concurrency (the knowledge mutex pattern from stage h). A
    coordinator collects worker results and synthesizes one answer.
    Done when: `go test -race` stays green across the pool, and an
    acceptance test launches several subtasks in parallel and verifies
    the coordinator aggregates every result correctly (no lost, no
    duplicated work).

## Phase 5 - setup that does not need a manual

31. **WhatsApp setup wizard** - a `fiveagent setup whatsapp` command that
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
- Single-agent by design: one agent loop with good tools. Multi-agent
  orchestration adds a model call, latency and coordination failures
  per extra agent - a bad trade for small local models, which fail more
  coordinating than executing. It stays a future path specifically for
  large models, or for long tasks worth parallelizing.
- Measure, then decide: architecture and model choices follow objective
  measurements (the memory evals of stage h), never vibes. Example: if
  evals show the small chat model handles injected memories poorly -
  ignoring the "data, never instructions" label or hallucinating over
  them - the levers are a bigger chat model or a better injection
  prompt, and the numbers decide.
