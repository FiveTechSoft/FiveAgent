# Choosing a model for FiveAgent (Ollama edition)

FiveAgent is model-agnostic: it talks to any OpenAI-compatible
endpoint. This short guide is what we learned running it on local
models through Ollama. Adopt or drop by your own battery runs, never
vibes: `FIVEAGENT_EVAL_LIVE=1 go test ./evals -run TestLiveBattery`
tells you the truth about a model on YOUR hardware.

## The short answer

- 12 GB VRAM: a 9B-class instruct model at q8 (e.g. qwen3.5:9b q8)
  usually beats a 14B at q4 at equal VRAM. Quantization quality is
  measurable; run the battery on both and keep the winner.
- CPU only: it works, but expect tens of seconds per reply. Set
  `timeout: 600` (the default for local endpoints) and keep the model
  small.
- A second, bigger model as `coder:` pays off for code requests: the
  agent routes code-looking messages to it automatically.

## Settings that matter more than the model

- **Sampling (stage 11a)**: `sampling.tool_temperature: 0.1` for
  tool-calling rounds, `sampling.chat_temperature: 0.6` for the final
  answer, `sampling.presence_penalty: 0.3` against the repetition
  loops small models fall into. Unset fields keep provider defaults.
- **num_thread vs chat_template_kwargs**: `num_thread` rides Ollama's
  native /api/chat route; `chat_template_kwargs` rides the /v1 route.
  Both on one model fails at load - keep one per model.
- **Thinking models**: reasoning models can spend the whole turn
  budget thinking and reply empty. If yours does,
  `chat_template_kwargs: { enable_thinking: false }` on the /v1 route.

## What a small model cannot do

- See images: text-only models cannot. For inbound photos you need a
  VL variant that fits your VRAM (see `describer_url` in the example
  yml).
- Reliable long tool chains: keep tools few and well described; every
  extra tool degrades a small model.

## When in doubt

Run the battery. The report (minted as a signed link at the end of the
run) shows pass/abstention/miss/hallucination per category; the gate
that matters is zero real hallucinations.
