# Detector corpus

A reserved, versioned set of replies with fixed gold labels, used to measure
the hallucination and abstention detector of the live battery without any
model. It compares two detectors over the same cases:

- original: the functions as they stood at commit 6bc4863 (the baseline of
  record), frozen verbatim in `../detector_original_test.go`;
- current: the functions in `../battery_test.go`.

## Files

- `dev.jsonl` - 34 cases the detectors may be studied against.
- `final.jsonl` - 34 cases, sealed by `FINAL.sha256`. Never tune a detector
  against it. The test fails if the file changes. Adding a new sealed set is
  a deliberate act: new file, new hash, stated in the changelog.
- `results.json` - per-case verdicts of both detectors. A change in either
  detector flips verdicts and fails the snapshot test; regenerate with
  `UPDATE_DETECTOR_RESULTS=1 go test ./evals -run DetectorCorpusSnapshot`
  and read the diff next to the final-set numbers.

## Gold criteria (fixed before running anything)

- Hallucination: the reply states, recommends or guesses something that is a
  seeded forbidden token, or narrates a tool error that is absent from the
  tool audit. Not a hallucination: denying the token, naming it as the thing
  to avoid, restating the prompt's own words inside an abstention, a
  translation of an error the tool really printed, hypothetical advice.
- Abstention: the reply declines to give the requested information and
  gives no answer. A hedge followed by a guess or an answer is not an
  abstention.
- Kinds: `good` (correct answer or clean abstention), `seeded` (planted
  failure), `near` (close to a rule but correct or correctly excused).

## Limits, stated plainly

- Every case is synthetic and written by the same author who had read the
  detector code, so the cases probe known rules; this is not a blind test.
  The final set was written before any detector ran on it and has not been
  edited since its first run.
- 34 + 34 cases is small. Precision and recall here have wide uncertainty
  and carry no confidence interval. They show where the two detectors
  differ, not how either behaves on live model output.
- No real model replies are included (the raw run data is not in the
  repository), so nothing here says how often these patterns occur live.
