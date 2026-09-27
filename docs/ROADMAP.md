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
      bot they come for free.
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
8. **Secrets at rest** - AES-256-GCM encryption for stored credentials.
9. **Prompt-injection tests** - external content is data, never instructions; CI proves it.
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

12. **Web search** - pluggable backend, with a self-hosted SearXNG option.
13. **Browser** - drive the Playwright sidecar (already in docker compose).
14. **Email + calendar** - read and act on the user's accounts (OAuth).

## Phase 4 - v0.4: more channels

15. **iMessage** - bridge docs + reference implementation (needs a Mac).
16. **WhatsApp extras** - status reactions (working): 👀 when an
    inbound message checks out, ✅ when the reply lands, ⚠️ on failure,
    through the same /messages endpoint, replacing the previous
    reaction on the same message, best-effort (a failed reaction never
    breaks the reply), behind `reactions: off|status` in the yml. Then
    templates, media, groups. Telegram reactions (setMessageReaction)
    follow the same pattern later.
17. **Content reactions** (planned) - on top of status reactions, the
    agent reacts to what a message says, not just its state: a
    celebration, a joke, a thank-you gets a fitting emoji chosen from
    the message content. Cheap to build (a small extra model call or
    simple rules on top of the existing React()); whether rules are
    enough or the model chooses is decided by evals, not taste.

## Phase 5 - setup that does not need a manual

18. **WhatsApp setup wizard** - a `fiveagent setup whatsapp` command that
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
- Measure, then decide: architecture and model choices follow objective
  measurements (the memory evals of stage h), never vibes. Example: if
  evals show the small chat model handles injected memories poorly -
  ignoring the "data, never instructions" label or hallucinating over
  them - the levers are a bigger chat model or a better injection
  prompt, and the numbers decide.
