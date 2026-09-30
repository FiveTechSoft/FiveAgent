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
   Candidate documented in CHANGELOG.md (2026-09-29); the tag waits
   for battery run 6 (baseline) against HEAD with the agreed gate of
   zero real hallucinations.

## Phase 2 - v0.2: memory and safety

7. **File-based long-term memory** (planned) - the agent remembers
   beyond the last 20 messages using plain markdown files with git as
   the source of truth: readable, versioned, no extra infrastructure,
   and an LLM reads markdown natively. It grows in stages:
   a. **Minimal memory** (working) - the files people.md,
      preferences.md and workstreams.md, with a git commit on every
      write (stages f and g later added learnings.md and digests.md to
      the set).
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
   f. **Episodic memory** (implemented, 2026-09-30, CI-tested) - a
      learnings.md where the agent writes a short self-critique in
      plain words when a task fails or the user corrects it ("no, the
      other Taylor"), recalled as data in later turns. User
      corrections are the evaluation signal, and in a chat bot they
      come for free. Shipped slice: the save_learning tool writes the
      lesson through Knowledge.Append (deduped, capped, one plain
      bullet); inbound emoji reactions are recorded too: the adapter
      keeps the wamid-to-reply mapping at send time (bounded at 500),
      a 👎/👍/❤️ on a reply we sent lands as a learning in the
      SENDER's own scope (users/<sender>/learnings.md - one user's
      feedback never surfaces for another), and reactions to messages
      we did not send or with other emojis are logged only. (Inbound
      reactions are intercepted before the agent: they never trigger
      a turn.) Feeding learnings back into eval scenarios stays
      pending.
   g. **Rolling session digest** (implemented, 2026-09-30, CI-tested) -
      the stage 15 pruner compacts the middle turns of an over-budget
      conversation into a summary; that summary now ALSO lands in the
      sender's own digests.md (users/<sender>/), recalled as data in
      later turns, so detail dropped from the live history survives as
      memory. The pruner reports the summary through a DigestSink hook,
      called only when the summarizer pass succeeds - the omission-
      marker fallback writes nothing ("N turns omitted" is noise, not
      memory). The digest lives in the per-sender scope, never the
      global memory: one sender's compacted conversation cannot leak
      into another sender's recall. evals/digest_test.go drives 24
      verbose turns through the real agent, proves the bullet lands on
      disk, proves a later query recalls it, and proves a second
      sender asking the same question sees no digest.
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
      abstention, not invention), M7 tool-error narration fidelity
      (implemented 2026-09-29: when a tool call errors, the reply's
      narrated error must match the error the audit trail observed -
      the inventedErrorNarration detector compares E_ codes and
      access-denied phrases against the run_command audit delta,
      whitespace-free to catch the spaced-letters degradation, and
      scores a mismatch as a hallucination; pattern from run 4's
      invented E_ACCESDENIED vs the real CreateProcess
      file-not-found). Evolution is measured, not told:
      every live run dumps its metrics to a JSON artifact under
      evals/out/ (stage 12's trajectory logger becomes their natural
      transport), a historical scorecard in evals/README.md tracks the
      numbers run over run, and a memory metric that drops versus the
      last scorecard with the same model blocks the "done" of the
      stage that caused it until the drop is explained. Hallucinations
      stay the hard gate of every run.
   n. **Automatic background indexing** (implemented, 2026-09-29,
      CI-tested) - memory no longer grows
      only when the model decides to save (save_memory /
      forget_memory). The complementary path: a background
      indexer absorbs conversations into the memory files with no
      model decision at all - the system remembers on its own, and
      the manual tools stay for deliberate curation. Indexing is per
      user from day one: each sender's conversations index into their
      own scope (links with 7i's scoping decision and the per-user
      sandbox of stage 3). The files stay the single source of
      truth; the indexer is a writer, not a second memory.
      internal/agent/indexer.go: a bounded queue plus a background
      worker; a cheap heuristic pre-filter skips trivia, then ONE
      small extractor call per non-trivial turn (declared cost)
      writes plain bullets through Knowledge.Append only - no YAML
      headers, no structure, newlines stripped, 200-char cap, so
      injected content cannot rewrite memory layout; the exchange
      travels delimited as DATA in the extractor prompt. Model
      failures and a full queue log and drop, never delay the reply.
      The 7i question narrows honestly to "should manual saves also
      scope?" - documented, not silently decided: save_memory and
      "recuerda:" still write to the global scope while the indexer
      writes per sender, and recall merges global + own scope.
      memory.auto_index: false opts out.
      Done when: a scripted conversation that mentions a fact (no
      "recuerda:" anywhere) recalls it in a later session without a
      single save_memory call; the battery grows an "auto-index"
      case proving the fact landed through the indexer, and that one
      sender's indexed facts never surface for another.
8. **Secrets at rest** (implemented, 2026-09-29, CI-tested) -
    AES-256-GCM encryption for stored credentials. internal/secrets:
    32-byte key (base64/hex), self-describing JSON envelope, wrong
    key and tampering fail loudly via GCM authentication. Key source:
    FIVEAGENT_MASTER_KEY env (strongest: key never touches the disk)
    or data/master.key (0600, auto-created; secrets.key_file /
    secrets.key_env in the yml, "off" disables explicitly). The token
    store seals on save and migrates existing plaintext on first
    load without loss (parse first, then re-seal; a failed re-save
    never blocks the read). Threat model stated honestly in code and
    docs: a key file on the same disk protects copies of the data
    file (backups, sync clients, leaked archives), NOT the live
    machine - that is what the env key is for.
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

12. **Trajectory logging** (implemented, 2026-09-29, CI-tested) -
    every turn is recorded as a JSONL trajectory in the message shape
    the fine-tuning dataset (stage 13) consumes: user/assistant/tool
    messages with the tool calls and their results, the outcome
    (reply, duration, tool rounds, error) and per-tool stats
    (count/ok/fail). Real sessions opt in via trajectory.enabled in
    fiveagent.yml (rotating trajectories.jsonl, bounded at MaxMB x
    MaxFiles); battery runs write one JSONL per case plus a
    tool-stats.json summary, and trajectory.Compare diffs two runs by
    their artifacts (the battery rotates run dirs last/prev and logs
    the comparison). Nothing sensitive lands on disk: every recorded
    string is redacted (emails, phone-shaped numbers, bearer tokens,
    token/secret key-values) before any sink sees it, and the record
    carries the channel, never the user's identity. A run of the
    battery becomes a durable, comparable artifact: what the model
    did, not just pass/fail.
    Done when: (met) the battery writes one JSONL trajectory per case
    plus the stats summary and compares runs by artifacts;
    evals/trajectory_test.go is the "trajectory" case - a scripted
    turn produces a schema-valid trajectory (roles, tool calls,
    outcome, stats) with a phone number redacted out of the record.
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
17. **Keyword-triggered context** (implemented, CI-tested) - skill
    and domain instructions enter the context only when the message
    mentions their trigger words, never by default. Sibling of the
    memory recall-by-alias: the context only pays for what the turn
    needs. internal/agent/agent.go gains the Skill type (name,
    triggers, loader) and WithSkills; long triggers match as
    substrings, short ones (<=3 chars) need a word boundary so "fwh"
    does not fire inside a longer word. The FiveTech domain reference
    is the first skill (DomainSkill, triggers harbour/fivewin/fwh/
    fivetech/fivegui/xharbour): it used to ride in every system
    prompt (2.9KB per turn) and now enters only on matching turns.
    Scope note: skills are defined in code; the skills/ folder format
    with one SKILL.md per skill landed at stage 32, and
    docs/fivetech-domain.md migrated then as planned.
    Done when: (met) the scripted trigger cases (evals/trigger_test.go)
    capture the exact request the model receives and measure the
    injected bytes: an unrelated turn carries zero skill text, a
    matching turn carries the domain block exactly once (2959 bytes),
    and a coverage guard proves every harbour_fivewin battery prompt
    triggers the skill (otherwise the triggered design would silently
    un-ground those answers). The live battery keeps its domain
    coverage through the same DomainSkill.
18. **Subordinate agents** (implemented, CI-tested) - the agent
    splits a big task into small subtasks and runs each in an
    isolated subturn with its own fresh context, then composes the
    results. The single most effective harness technique for small
    models: no subtask exceeds what the model can do in one clean
    turn. internal/agent/subtask.go: the model drives the split
    through the run_subtask tool (self-registered by agent.New); each
    call runs one subtask with fresh context - no history, no memory
    recall, no store writes - the same model and tools, and every
    tool EXCEPT run_subtask, so delegation is capped at depth 1.
    Subturns go through the stage-16 recovery ladder and the stage-14
    argument repair like any turn. Sequential only, no parallelism
    (that is stage 34).
    Done when: (met) the scripted battery case
    (evals/subtask_test.go) plays both sides of a multi-step task and
    proves the machinery: the main turn delegates, the subturn
    request carries the subtask text but NOT the original prompt
    (fresh-context isolation) and no run_subtask spec (depth cap),
    and the final reply composes the subturn result; a still-empty
    subturn surfaces as a tool error, never a hang. The live battery
    grows a subordinate: category measuring the outcome in vivo; the
    model's own decision to decompose is the live side, pending the
    next run.
19. **Durable delivery ledger** (implemented, CI-tested) - every
    outbound reply is recorded as a persistent delivery obligation
    (pending -> attempting -> delivered) BEFORE the send is
    attempted, so a crash between generating and sending never
    silently loses a reply. internal/channel/ledger.go: a JSON-file
    ledger (data/deliveries.json, configurable via delivery.path)
    shared by the Telegram and WhatsApp adapters; on startup each
    adapter redelivers its pending entries exactly once, prefixed
    with a visible recovered marker. Attempts are capped (5, then
    dead) and entries undelivered after 24 h expire instead of
    arriving confusingly late. Scope note: a crash can land after
    the platform accepted the message but before the ledger recorded
    it, so a redelivery can repeat a message the user already got -
    the marker exists exactly for that case.
    Done when: (met) the battery's delivery case
    (evals/delivery_test.go) kills a send mid-flight over a crash
    fixture and proves the next start redelivers exactly once with
    the marker, the obligation is durable before the send, a dead
    entry is never retried, and a stale entry expires unsent.
20. **Web search** (implemented, CI-tested against fake servers; pending
    first live run) - the `web_search` tool with pluggable providers:
    DuckDuckGo by default (no API key, may rate-limit under heavy use),
    Brave via `web_search.api_key` for production. `web_search:` section
    in the yml. A self-hosted SearXNG provider remains a welcome option.
21. **Workspace tools** (implemented, CI-tested) - real filesystem
    tools for the agent: read_file, write_file and edit_file with
    true diffs (the model passes old/new text, the tool verifies the
    exact context matches exactly once and returns the applied diff;
    unknown or ambiguous old_text is refused without touching the
    file), all scoped to the user's folder
    (data/workspace/<channel>-<user>, workspace.enabled in the yml) -
    path escapes are flattened inside however they are written. Every
    write snapshots the prior state with go-git (already a
    dependency), so every edit is undoable and auditable - no more
    destructive shell redirects. Base for the later git tool.
    Done when: (met) the scripted battery case
    (evals/files_test.go) drives the write/read roundtrip, the
    true-diff edit with its exact-match verification, path-escape
    flattening, per-user isolation, and the snapshot chain (three
    writes leave two prior-state commits holding the earlier
    contents). The live battery grows a files: case measuring the
    model driving the tools in vivo; the first end-to-end file edit
    over real WhatsApp remains pending live verification on a live
    install.
22. **Cron scheduler** (implemented, CI-tested) - scheduled
    automations in natural language, delivered to the originating
    channel. internal/sched: a JSON-file job store (data/jobs.json,
    cron.enabled in the yml) inside the agent process; the model
    registers jobs through the schedule_job tool (one-shot via
    in_minutes or deliver_at, recurring via every_minutes) and a
    15-second ticker fires them unattended, delivering the text back
    to the same WhatsApp/Telegram chat with an ⏰ prefix. Every job
    is auditable in the file: what runs, when it last ran, how many
    times. Scope: a failed delivery is retried on the next tick; a
    crash between delivery and persist can refire once - the stage-19
    ledger integration for scheduled sends is future work. Richer
    schedules (daily at HH:MM, weekdays) build on the same store.
    Done when: (met) the scripted battery case (evals/cron_test.go)
    creates a one-shot, fires it, and verifies the exact delivery
    text, the audit trail, no refire on the next tick OR after a
    restart (reloaded from the file), recurring next-run advance, and
    the tool's refusal of bad timing args. The live battery grows a
    cron: case measuring the model registering and confirming in
    vivo; the first real WhatsApp reminder end-to-end remains
    pending live verification on a live install.
23. **Browser** (implemented, CI-tested; simple-first cut) - a web
    browser as a native tool, OUTSIDE the command sandbox (which
    stays offline). Simple first: a pure-Go engine (HTTP fetch +
    golang.org/x/net/html, already a dependency) instead of a
    Playwright sidecar - single binary, no runtime, fully CI-testable
    end-to-end. Pages arrive as a numbered list of interactive
    elements and the agent acts by id ("fill #3", "click #7") -
    automation by structure, never pixels: small models fail at
    vision-coordinate clicking but handle structured elements well.
    No JavaScript: pages that need it are the Playwright upgrade,
    which stays the documented path for JS-heavy sites. Safety by
    design: EVERY form submit is gated - the tool returns a
    single-use token and a summary, the model asks the user, and only
    confirm(token) sends (tokens expire in 10 minutes, wrong or
    reused tokens send nothing); password values are redacted from
    tool results, the gate summary and the audit log; every
    open/fill/click/submit lands in a per-user audit log
    (data/browser-audit, browser.enabled in the yml); one browsing
    session per user. Still pending from the original sketch: a real
    credential vault (today: redaction only), per-skill domain
    allowlist, downloads into the user's folder.
    Done when: (met) the battery's web cases
    (evals/browser_test.go) fill a local test form end-to-end -
    browse, fill, gated submit, confirm - and the confirmation-gate
    eval proves the purchase form reaches the server ONLY after
    confirm: never on submit, never on a wrong token, never twice on
    the same token; the audit eval proves the password value appears
    in no log line. Live model side pending the next run.
24. **Links** (implemented, CI-tested) - the bot answers with links
    served by its own HTTP listener (mounted on the same mux as the
    WhatsApp webhook, localhost-bound unless a tunnel exposes it),
    not with wall-of-text messages:
    a) Reports: make_report_link mints a signed link to a clean HTML
       page. (The battery-report first use case arrives when the
       runner generates its report through the tool.)
    b) Data collection: make_form_link mints a signed link to a small
       form; what the user types goes straight to the vault
       (data/vault/<key>.secret, 0600), never through the chat and
       never into any log line.
    Security by design (implemented): every link is HMAC-signed
    (secret generated once into data/links-secret), binds the sender
    it was minted for, and expires in 24 h; the PIN that goes to the
    sender's chat alongside the link is the second factor - another
    sender opening the link does not have it; links are minted only
    in a user context (the tool refuses without one); the form
    handler never logs submitted values (audit lines say "never
    logged").
    Done when: (met) the battery's links cases
    (evals/links_test.go) prove (i) a report link renders and is
    rejected after expiry (410) or with a tampered signature (403),
    (ii) a form submission lands in the vault and its value appears
    in no log line, (iii) a link opened against another sender's
    record fails signature verification (403) and a missing/wrong
    PIN is rejected (403). Live model side pending the next run.
25. **Media** (partially implemented, 2026-09-29) - WhatsApp media
    pipeline in BOTH directions: the webhook receives
    image/audio/video/document with a media id, downloads it with an
    authenticated Graph API call, dispatches by type, and the result
    enters the normal message flow; outbound, the bot synthesizes
    voice and images and sends them as native WhatsApp media (upload +
    send via Graph API).
    Shipped slice: the pipeline skeleton. Inbound voice notes and
    images are downloaded with the authenticated two-step Graph call
    and dispatched to pluggable processors - a whisper.cpp server for
    audio (transcriber_url in the yml) and any OpenAI-compatible
    vision endpoint for images (describer_url/describer_model), so the
    agent binary stays pure Go and the heavy models run as separate
    services. When a processor is not configured or fails, the agent
    gets an honest bracket note ("[voice note - transcription not
    configured]"), never a silent drop or a fake transcript. Outbound,
    UploadMedia + send-by-id round-trip through the Graph API, and
    phase d) first cut: with tts_url pointing at an OpenAI-compatible
    /v1/audio/speech endpoint (openedai-speech for Piper,
    kokoro-fastapi for Kokoro - one client covers both), replies go
    out as native voice notes; any synthesis/upload/send failure falls
    back to the text reply, and agent-failure fallback strings always
    go as text. Per-user opt-in ("respondeme por voz") is pending; the
    toggle is global per deployment today. Phase e) first cut: the
    send_chart tool renders bar/line charts in pure Go (stdlib +
    x/image bitmap font, no cgo, no services) and sends them as native
    images (upload + send by id, optional caption) on media-capable
    channels; channels without media support answer the model with an
    honest error, and send failures never report success. Battery
    report charts and diagrams remain pending variations of the same
    renderer. Phase c) first cut: with ffmpeg available (external
    tool, ffmpeg_path in the yml or on PATH) inbound videos are
    downloaded and split into key frames + a 16kHz WAV track, then the
    EXISTING transcriber and describer processors turn them into
    "[video] audio: ... | frames: ...". With no processors configured
    the agent gets an honest "analysis not configured" note and no
    megabytes are downloaded; with no ffmpeg the plain "[video]"
    announcement stays. Tests fake the binary (CI has no ffmpeg) and
    prove the pipeline and its honest fallbacks.
    internal/channel/whatsapp_media_test.go proves both directions
    against a fake Graph server with stub processors, asserts the
    Authorization header on every Graph call, and asserts failure
    paths surface honestly. Pending live verification with real
    whisper/VL/TTS services (the fake-server round trip cannot prove model
    quality), plus the rest of e) and live verification of c).
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
    (fake) Graph server confirms the upload and the send. The fake-
    server half of that is done as Go integration tests (above); the
    live half needs real whisper/VL services configured.
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
27. **Integrations: Gmail, Google Calendar, Drive, Slack, GitHub**
    (implemented, 2026-09-29: shared pattern + Gmail, Calendar,
    Drive, Slack, GitHub copies) -
    the agent reads and acts on the user's real accounts,
    one integration per service behind a shared pattern (OAuth
    connect, read tools, write tools), so the next service is a
    known shape, not a new design:
    a. **Gmail** - OAuth connect, read (search, threads, attachments)
       and write (drafts, send, reply).
    b. **Google Calendar** - OAuth connect, read (events, free/busy)
       and write (create, update, invite).
    c. **Drive** - OAuth connect, read (search, download) and write
       (upload, share).
    d. **Slack** - OAuth connect, read (channels, DMs, threads) and
       write (post, reply).
    e. **GitHub** - OAuth connect, read (issues, PRs, repos) and
       write (comment, review).
    Every integration inherits the same rules: external content is
    data, never instructions (stage 9), secrets live encrypted at
    rest (stage 8, shipped), and outbound actions ride the delivery ledger
    (stage 19).
    Shipped slice (27a): the shared pattern - internal/oauth
    (authorization-code flow, refresh, 0600 token store with atomic
    writes, /oauth/<service>/start + /callback handler mounted like
    links) - plus Gmail: gmail_search and gmail_send tools, the
    connect flow, and transparent token refresh. Everything is tested
    against fake endpoints (form fields, state rejection, store
    permissions, RFC822 payloads, auth headers); no real account is
    ever touched in CI. The token store is encrypted at rest by
    stage 8 (shipped). Pending: live verification
    with a real Google OAuth app (needs the operator's client
    credentials), 27b shipped as the pattern copy:
    Calendar list/create tools, FreeBusy in the client, same
    store/handler/honest-error shape, tested against fake endpoints -
    the pattern is proven, not just claimed. Pending: merging Google
    scopes into one consent, and Slack / GitHub as the next copies.
    27c shipped as the third copy: Drive list/download/upload tools
    on the drive.file scope (the agent sees only its own files - a
    personal agent browsing the whole Drive is a bigger trust
    decision than this stage takes), same store/handler/honest-error
    shape, tested against fake endpoints. 27d shipped as the first
    non-Google copy: Slack channels/history/post tools, the
    ok:false-on-200 quirk handled in code, non-expiring bot tokens,
    same store/handler/honest-error shape - the pattern now stands
    outside one vendor's API style. 27e shipped as the last
    planned copy: GitHub repos/issues/create-issue tools, PR marking,
    per-provider token request headers (GitHub's form-encoded token
    quirk), non-expiring tokens - the stage's done-when is fully met:
    five providers, three API styles, one shared shape.
    Done when: one integration ships end-to-end (OAuth connect ->
    read -> write) with its battery case, and the second integration
    lands as a copy of the shape, proving the pattern.

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

32. **Skills** (implemented, 2026-09-29, CI-tested) - the `skills/`
    folder holds one SKILL.md per skill in its own subfolder: a
    `name:`/`trigger:`/`keywords:`/`tools:` header and a concise
    procedure body written for a small model. Only the one-line index
    (name + trigger per skill) enters the system prompt on every turn;
    the full body loads on demand when a keyword matches (the stage 17
    machinery), and a tool named in `tools:` is offered only on turns
    where its skill triggered. docs/fivetech-domain.md migrated to
    skills/fivetech/SKILL.md as the first skill; on-disk files win
    over the build-time embedded copies (skills/embed.go), so the
    library is editable without a rebuild, and one malformed file is
    skipped with a log line instead of taking the library down. This
    is how the small model "learns from the big ones": expertise is
    written down once instead of re-derived per session. Done when:
    (met) evals/skills_test.go runs the harbour_fivewin battery
    prompts against a scripted player that answers with the verified
    facts only when the turn carries the reference and confabulates
    the live failure modes (FireWall Helper, cVar, Input()) when it
    does not: 4/4 pass with the skill loaded, 0/4 without - the
    wiring carries the skill and the skill carries the facts.
33. **FiveAgent as MCP server** (implemented, 2026-09-29, CI-tested) -
    a local, token-authenticated MCP endpoint (POST /mcp, JSON-RPC
    2.0, streamable-HTTP shape: initialize, ping, notifications,
    tools/list, tools/call) so an external agent - the design
    consumer is the owner's OpenCode - runs commands inside the
    sandbox (fiveagent_run_command, raw argv, stdout+stderr+exit code
    back, non-zero exit as an honest isError) and reads files from
    the workspace folder (fiveagent_read_file, ".." escapes refused).
    Confinement is by construction: the package's only execution path
    is Sandbox.Run and it never execs directly. A missing or wrong
    bearer token gets a 401 with a JSON-RPC error body. docs/mcp.md
    takes a reader from zero to the first call (yml block, client
    config snippet, curl smoke test) and the README carries the MCP
    client snippet. Done when: (met) evals/mcp_test.go drives the
    endpoint over real HTTP with a fake client and a fake sandbox -
    auth refusals, the full protocol flow, commands reaching the
    sandbox as argv under the mcp user key, reads confined, every
    failure mode honest. The live check with the owner's OpenCode is
    on the user's verification queue.

34. **Multi-agent parallel execution** (implemented, 2026-09-29,
    CI-tested) - run_subtasks fans SEVERAL independent subtasks out
    through a bounded worker pool (goroutines fed by a task queue,
    cap 4 concurrent, cap 8 per call) on the stage 18 subturn
    machinery: each subtask is the same isolated subturn - fresh
    context, nothing mutable shared - and delegation stays capped at
    depth 1 (subturns see neither run_subtask nor run_subtasks). One
    failing subtask fills its own slot with an honest, attributed
    error; the others still finish. The main turn is the
    coordinator: it receives every result in order and synthesizes
    the answer. Sequential run_subtask stays for dependent steps.
    Done when - MET: `go test -race` green across the pool; the
    "parallel" battery fans 3 subtasks whose fake subturns sleep
    250ms each - the whole turn takes ~260ms, not the ~750ms
    sequential would, proving real overlap from wall time; the
    coordinator's request carries every aggregated slot; no
    subturn's request offers the delegation tools.

35. **Proactivity layer** (implemented, 2026-09-29, CI-tested) -
    the agent wakes up on its own
    when something happens: subscriptions to sources (an email
    arriving, a document changing, a calendar event starting) fire
    the agent instead of waiting for the user to speak. Time-based
    wakes ride the cron scheduler (stage 22); a fired subscription
    runs a turn and its reply goes out through the delivery ledger
    (stage 19), so a crash loses nothing. Every subscription is
    auditable (source, filter, what it triggered) and expires or
    pauses cleanly. internal/proactive: a JSON-backed subscription
    manager (same shape as the cron store); adapters call Notify,
    matching subscriptions run one turn and the reply goes out
    through the ledger (stage 19), so a crash loses nothing. Time
    wakes are NOT duplicated here - they stay with cron (stage 22).
    Dedup: each subscription remembers the last 50 event ids; a
    source redelivery never double-fires or double-delivers. Audit:
    source, filter, instruction and a bounded trail (50) of what it
    triggered; expiry is lazy and final, pause/resume anytime. A
    failed turn stays unseen so a source redelivery retries; once
    the turn ran, the fire is recorded even if delivery fails - the
    ledger owns reply recovery from there. The model manages its
    subscriptions with subscribe / subscriptions /
    pause_subscription / unsubscribe; a user can only touch their
    own. Live sources (real Gmail/Calendar watches) are pending live
    verification with the user's credentials.
    Done when - MET: the "proactive" battery case fires a scripted
    gmail event with no user message, the model request carries the
    wake marker and the event summary, the reply lands delivered
    through the real ledger, and an identical event neither
    re-runs nor re-delivers.
36. **Unified conversation** (implemented, 2026-09-29, CI-tested) -
    one conversation per user
    across channels, not one thread per channel: the user starts on
    WhatsApp, continues on Telegram, and the agent sees a single
    history and a single memory scope. Channel identities resolve to
    one user identity through an explicit linking flow, never
    guessed; each reply still lands on the channel the user wrote
    from. internal/identity: a JSON-backed link store. The flow is
    explicit and mechanical: link_channel mints a one-time code
    (10-minute expiry) on the current channel, the user writes
    "vincular <code>" from the other channel, and the agent redeems
    it WITHOUT a model turn; a code from an already-linked identity
    points at its root, so chains converge. Linked identities share
    one history and one auto-indexed memory scope under the
    canonical identity; unlinked senders key off their own channel
    identity - separate by default, always. Delivery tools keep the
    original channel+user, so replies and scheduled jobs land on
    the channel the user wrote from. unlink_channel dissolves,
    linked_channels audits.
    Done when - MET: the "unified" battery links whatsapp+telegram
    with the code flow (zero model calls at redemption), a fact
    told on whatsapp surfaces in the telegram turn's history AND
    indexed memory scope, and an unlinked sender on the same
    channel sees none of it. This closes the gap list.

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
