# AGENTS.md - working on FiveAgent

Guidance for agents (human or AI) working in this repo.

## What this is

FiveAgent is a personal agent in Go: one binary, one config file
(`fiveagent.yml`), messaging channels (Telegram, WhatsApp Cloud API,
iMessage), long-term memory, a per-user command sandbox, and pluggable
tools. Single agent loop by design - see the principles in
docs/ROADMAP.md.

## Build and run

```
go build -o fiveagent ./app/fiveagent   # fiveagent.exe on Windows
cp fiveagent.yml.example fiveagent.yml  # then edit: model endpoint, channels
./fiveagent
```

`fiveagent.bat` (Windows) builds nothing but starts Ollama if needed and
logs everything to `fiveagent.log`.

## Tests

```
go build ./... && go vet ./... && go test ./...
```

House rules:

- No test touches a real service. Ever. Providers, the WhatsApp Graph
  API, search engines and model servers are all faked with
  `httptest.NewServer` (see `internal/tools/websearch_test.go` and
  `newTestWhatsApp` in `internal/channel/whatsapp_test.go`). A test that
  dials out is a bug: it once made CI flaky for real.
- CI runs build + vet + tests on ubuntu, windows and macos on every
  push. All three must be green before work is reported done.
- Docs and README only claim what is implemented AND verified. Untested
  paths are marked "pending live verification". No vapor.

## The eval battery

`evals/` holds the comparative battery (`battery.yaml`: 24 prompts, 6
categories, each with a scoring rubric) plus the memory eval suite.
`TestBatteryFileValidates` runs in CI; the live runs are env-gated:

```
FIVEAGENT_EVAL_LIVE=1 go test ./evals/ -run Battery -v
```

Judge mode (a reference model grades the local model's answers):

```
FIVEAGENT_EVAL_LIVE=1 FIVEAGENT_EVAL_JUDGE=1 \
  FIVEAGENT_EVAL_JUDGE_BASE_URL=https://api.openai.com/v1 \
  FIVEAGENT_EVAL_JUDGE_API_KEY=sk-... \
  FIVEAGENT_EVAL_JUDGE_MODEL=gpt-4o-mini \
  go test ./evals/ -run Battery -v
```

Live env vars: `FIVEAGENT_EVAL_BASE_URL` (default
http://localhost:11434/v1), `FIVEAGENT_EVAL_MODEL` (default qwen3.5:9b),
plus the `FIVEAGENT_EVAL_JUDGE_*` trio (judge API key falls back to
OPENAI_API_KEY; a localhost judge needs no key). The hard gate fails
only on hallucinations; everything else is reported as metrics.
Details and baseline numbers: evals/README.md.

## Map of the repo

- `docs/ROADMAP.md` - phases, numbered stages, measurable done-criteria.
- `docs/whatsapp.md` - WhatsApp Cloud API setup, paso a paso (Spanish).
- `docs/deploy-linux.md` - Linux server deployment (bubblewrap, systemd).
- `docs/fivetech-domain.md` - verified FiveTech/Harbour/FiveWin domain
  facts; the anti-confabulation seed.
- `evals/README.md` - how the battery works and the baseline.
- `deploy/` - systemd unit and the cross-compile helper.
