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
   provider and api_key from model when omitted. Later: more task
   classes beyond chat/code and fully configurable model sets.
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

12. **Web search** (implemented, CI-tested against fake servers; pending
    first live run) - the `web_search` tool with pluggable providers:
    DuckDuckGo by default (no API key, may rate-limit under heavy use),
    Brave via `web_search.api_key` for production. `web_search:` section
    in the yml. A self-hosted SearXNG provider remains a welcome option.
13. **Browser** (planned) - a headless Playwright browser as a native
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
14. **Links** (planned) - the bot answers with links served by its own
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
15. **Media** (planned) - WhatsApp media pipeline in BOTH directions:
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
16. **Email + calendar** - read and act on the user's accounts (OAuth).

## Phase 4 - v0.4: more channels

17. **iMessage** - bridge docs + reference implementation (needs a Mac).
18. **WhatsApp extras** - status reactions (working): 👀 when an
    inbound message checks out, ✅ when the reply lands, ⚠️ on failure,
    through the same /messages endpoint, replacing the previous
    reaction on the same message, best-effort (a failed reaction never
    breaks the reply), behind `reactions: off|status` in the yml.
    Burst debounce (working): rapid messages from one sender join a
    single turn (3s window, `debounce` in the yml), one reply with the
    full context in arrival order, quoting the last message. Then
    templates, media, groups. Telegram reactions (setMessageReaction)
    follow the same pattern later.
19. **Content reactions** (planned) - on top of status reactions, the
    agent reacts to what a message says, not just its state: a
    celebration, a joke, a thank-you gets a fitting emoji chosen from
    the message content. Cheap to build (a small extra model call or
    simple rules on top of the existing React()); whether rules are
    enough or the model chooses is decided by evals, not taste.

## Phase 4b - v0.4b: growing the small model

21. **Skills** (planned) - a `skills/` folder with one SKILL.md per
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
22. **FiveAgent as MCP server** (planned) - expose a local,
    token-authenticated MCP endpoint so an external agent (e.g. the
    owner's OpenCode) can execute commands inside the sandbox and read
    files from the user's folder. Documented in docs/. Done when: an
    integration test drives the endpoint with a fake client (auth
    rejected without token, command runs confined to the sandbox), and
    the doc page gets a reader from zero to first call in minutes.

23. **Multi-agent parallel execution** (future path, for large models;
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

20. **WhatsApp setup wizard** - a `fiveagent setup whatsapp` command that
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
