# Memory evals

How FiveAgent measures its long-term memory, instead of trusting vibes.

## Two tiers

**System evals (run in CI, deterministic).** A scripted fake model plays
both sides of scripted conversations, so the numbers say how the memory
*system* behaves:

- `TestRecallSurvivesTruncation` - a fact stored on turn 1 must be
  injected on turn 32, past the 20-message history window.
- `TestCorrectionSticks` - correcting a fact removes the old value and
  injects the new one.
- `TestInjectionPrecision` - a query must not drag unrelated memories
  into the context (relevant lines / total injected lines).
- `TestDuplicateSaveRejected` - the same fact saved twice lands once.
- `TestInjectionCost` - chars and estimated tokens injected per turn,
  with a 4000-char guardrail.
- `TestRecallRate` - 10 distinct facts, 10 targeted queries: all must
  hit.
- `TestAliasRecall` - a query using a file alias ("contactos") injects
  the people file even when no body word matches.
- `TestInjectionCostScaling` - the injected block must stay bounded
  with a 50-note store, even when every note matches the query.

**Live model evals (manual quality gate).** `TestLiveModelMemory` runs
the same loop against a real model server, with the model itself
deciding what to save. Non-deterministic and needs a running model, so
it does not run in CI:

```
FIVEAGENT_EVAL_LIVE=1 FIVEAGENT_EVAL_MODEL=qwen3.5:9b go test ./evals/ -run Live -v
```

A roadmap stage is only "done" when its CI evals pass and the live gate
passes against the target model.

## Baseline (phase a, first run)

- recall past truncation: 1/1
- corrections: 1/1
- injection precision: 1.00
- dedup rejection: 1/1
- injection cost: 716 chars (~179 tokens) with a 10-note store
- recall rate: 10/10 targeted queries
- alias recall: 1/1
- injection cost at 50 notes: 726 chars (~181 tokens)

First catch: the precision eval proved the template description lines
("Who the user knows...") polluted recall for common words; recall now
scores fact bullets only.

Second catch: the scaling eval showed injection cost growing linearly
with matching notes (2956 chars / ~739 tokens at 50 notes, guardrail
breaks around 60). Recall now caps each file at its 10 most recent
matching bullets: 726 chars at 50 notes.
