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
- `TestForgetByAgent` - the model can delete a fact through
  forget_memory and it stops being injected.
- `TestHistoryIsolation` - one sender's conversation never leaks into
  another sender's request.
- `TestConcurrentSaves` - parallel saves from concurrent senders must
  all land: no silent fact loss on the git-backed store.

- `TestAbstentionPrompt` - the honesty rules must stay in the system
  prompt (born from a real confabulation: the model invented Harbour
  syntax and the meaning of FWH, the owner's own product).

**Live model evals (manual quality gate).** `TestLiveModelMemory` runs
the same loop against a real model server, with the model itself
deciding what to save. `TestLiveAbstention` replays the exact Harbour/
FWH confabulation: the answer must not contain the invented tokens and
must name FiveWin or abstain. Non-deterministic and needs a running model, so
it does not run in CI:

```
FIVEAGENT_EVAL_LIVE=1 FIVEAGENT_EVAL_MODEL=qwen3.5:9b go test ./evals/ -run Live -v
```

## The comparative battery

`battery.yaml` holds 104 prompts in 17 categories (harbour_fivewin,
general_knowledge, code, abstention, memory, tools, shell, c,
lenguajes, geografia, historia, instrucciones_compuestas, ciencia,
literatura, arte, logica,
web_search), each with a
grounded reference and a scoring rubric: must_contain / must_not_contain
/ abstain_ok / abstain_expected / setup. A setup turn carrying `memory_writes: <token>` is scored on the
disk, not on its reply: the memory files must hold the token after the
turn (metric M1, write-through; reported as a METRIC line, never a
gate). `TestBatteryFileValidates`
runs in CI and keeps the file honest (no empty categories, no prompt
without expectation). `TestLiveBattery` runs it against a live model
and prints the per-category report: pass, correct abstentions, misses,
hallucinations. The live gate fails ONLY on hallucinations (invented
tokens); the rest is metrics.

```
FIVEAGENT_EVAL_LIVE=1 go test ./evals/ -run Battery -v
```

**Judge mode (comparative).** With `FIVEAGENT_EVAL_JUDGE=1` each battery
prompt is also answered by a reference model (any OpenAI-compatible
endpoint - a big hosted model or a bigger local one), and the judge
grades the local reply against its own reference answer: 2 correct,
1 partial, 0 wrong or invented. The report adds a per-category judge
line: points and percentage of the reference. Judge scores are metrics,
not gate failures: the mechanical hallucination gate stays the only
hard fail.

```
FIVEAGENT_EVAL_LIVE=1 FIVEAGENT_EVAL_JUDGE=1 \
  FIVEAGENT_EVAL_JUDGE_MODEL=gpt-4o-mini \
  FIVEAGENT_EVAL_JUDGE_API_KEY=sk-... \
  go test ./evals/ -run Battery -v
```

Judge env: `FIVEAGENT_EVAL_JUDGE_BASE_URL` (default
https://api.openai.com/v1), `FIVEAGENT_EVAL_JUDGE_API_KEY` (falls back
to OPENAI_API_KEY), `FIVEAGENT_EVAL_JUDGE_MODEL` (default gpt-4o-mini).
A local judge (e.g. a 27b on Ollama) needs no API key.

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
- forget by agent: 1/1
- cross-sender history leaks: 0/1
- concurrent saves: 8/8 stored and recallable (was 5-7/8 before the
  store mutex: silent fact loss under parallel senders)

First catch: the precision eval proved the template description lines
("Who the user knows...") polluted recall for common words; recall now
scores fact bullets only.

Second catch: the scaling eval showed injection cost growing linearly
with matching notes (2956 chars / ~739 tokens at 50 notes, guardrail
breaks around 60). Recall now caps each file at its 10 most recent
matching bullets: 726 chars at 50 notes.

Third catch: the concurrency eval proved parallel senders lost facts
silently (5-7 of 8 saves landed, no error). Knowledge now serializes
reads and writes with one mutex: 8/8, race-detector clean.

## Live baselines

Official before/after comparison points. Each entry is a full live run
(`FIVEAGENT_EVAL_LIVE=1 go test ./evals/ -run Battery -v`) on the
owner's machine, reported verbatim.

### 2026-09-28 - tree d36a1b1 (first official baseline)

Score: **81 pass / 0 correct-abstention / 3 miss / 2 hallucination, of
95** (gate: FAIL on the 2 hallucinations). Unit suite the same day:
99 PASS / 0 FAIL / 3 SKIP (env-gated live tests). Run time 1270 s.

| Category | pass/total | Notes |
|---|---|---|
| abstention | 3/4 | 1 NO-ABSTENTION (detector gap, fixed after this run) |
| arte | 4/4 | |
| c | 6/6 | |
| ciencia | 5/5 | |
| code | 4/4 | |
| general_knowledge | 4/4 | |
| geografia | 5/5 | |
| harbour_fivewin | 2/4 | 2 HALLUCINATION (`cVar` in explanation tables) |
| historia | 5/5 | |
| lenguajes | 8/8 | |
| literatura | 3/3 | |
| logica | 10/11 | 1 MISS (riddle answered "mapa/piano", rubric wants "teclado") |
| memory | 7/15 | 8 forget/recall setups not scored |
| shell | 8/8 | |
| tools | 6/8 | 1 NO-ABSTENTION (detector gap, fixed after this run), 1 setup |
| web_search | 1/1 | |

Known issues found by this run, fixed in the commit that records it:

1. `abstains()` missed two real abstention phrasings ("no encontré
   información", "no tengo esa información") - detector gap, not model
   error. Both added with the "información" guard so "no he encontrado
   errores" stays a non-abstention.
2. `cVar` hallucinations: the model wrote correct code (`cNombre`) but
   fell back to `cVar` in the explanation tables. The domain doc now
   states the generic-token prohibition covers tables, inline examples
   and comments, not just code blocks.

Source: owner's live-run report, 2026-09-28 (double counting method,
Go validator and Python counter agreeing).

### 2026-09-28 (run 2) - tree 425a458 (first harness round after the baseline)

Score: **83 pass / 2 correct-abstention / 0 miss / 2 hallucination, of
96** (85/96; gate: FAIL on the 2 hallucinations). Unit suite the same
run: 99 PASS / 0 FAIL / 3 SKIP. Memory: **M1 write-through 7/7** (the
model really writes - the recuerda:/olvida: markers fixed the baseline
gap), **M3a effective forgetting: PASS**.

The 3 baseline misses (logica, tiddlywinks, tools) are clean. The
detector and domain fixes landed as predicted (81 -> 83 pass).

Post-run analysis (fixed in the commit recording this row):

1. One of the 2 hallucinations was a RUBRIC false positive, not a model
   error: the tiddlywinks abstention echoes the question ("No tengo
   información sobre quién ganó...") and the must_not_contain token
   "ganó " lives in the prompt. The matcher now tolerates prompt-echoed
   tokens inside abstentions only; novel tokens still count.
2. The real residual hallucination: `cVar` in an explanation table
   (harbour_fivewin) - improved 2 -> 1 versus the baseline. This is the
   number to beat in the next harness round, without touching weights.

Next-run expectation with the matcher fix: 86 + however many of the 8
new instrucciones_compuestas cases pass, over the new 104 total, with 1
hallucination. Those 8 cases have never run live - their pass count is
an open measurement, not an estimate.
