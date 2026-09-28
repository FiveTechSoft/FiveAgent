// escalation.go - the escalation decision module (stage 10a of
// docs/ROADMAP.md). This is a core security piece: it is the single
// point that decides whether a turn may leave the machine for a
// commercial provider.
//
// Guarantees by construction:
//
//   - Pure and deterministic: same signals and same config always
//     produce the same decision. No randomness, no time, no globals.
//   - Zero imports and zero I/O: this file cannot reach a network, a
//     disk, or anything else. It only maps signals to a decision.
//   - No path with opt-in off: with EscalationConfig.Enabled false the
//     decision is provably always "stay local" (see the exhaustive
//     decision-table test). Today no provider client exists at all;
//     when one is added, it must be wired so it is only ever invoked
//     after DecideEscalation returns Escalate=true.
//
// The harness decides; the model can only raise its hand. Escalation
// fires on measured evidence of local failure (an abstention, a tripped
// repetition guard, a repair storm, a failed verification). The model's
// self-declared uncertainty is a weak signal: it is recorded in the
// reason for observability, but alone it never escalates - small
// models self-evaluate poorly, and a wrong guess here either leaks a
// prompt or wastes money.
package agent

// EscalationSignals is the measured evidence of one local turn.
type EscalationSignals struct {
	// Abstained is true when the local model abstained (the honesty
	// rules fired: it said it does not know instead of inventing).
	Abstained bool
	// RepetitionGuardTripped is true when the stage 14 guard aborted
	// the reply as a degenerate echo.
	RepetitionGuardTripped bool
	// Repairs is the number of tool calls the stage 14 argument
	// repairer had to rescue during the turn.
	Repairs int
	// VerificationFailed is true when the turn's verification step
	// rejected the reply (stage 11).
	VerificationFailed bool
	// ModelUncertain is the model's own self-declared uncertainty.
	// Weak signal: recorded, never decisive on its own.
	ModelUncertain bool
}

// EscalationConfig is the user's opt-in. Disabled is the default and
// means no turn ever escalates, whatever the signals say.
type EscalationConfig struct {
	Enabled bool
	// RepairThreshold is the number of rescued tool calls in one turn
	// that counts as a repair storm. Values <= 0 use the default of 3.
	RepairThreshold int
}

// EscalationDecision is the verdict plus a human-readable reason that
// gets logged like any other turn event.
type EscalationDecision struct {
	Escalate bool
	Reason   string
}

// DecideEscalation maps turn signals and the user's opt-in to the
// escalation decision. Pure and deterministic by construction.
func DecideEscalation(s EscalationSignals, cfg EscalationConfig) EscalationDecision {
	if !cfg.Enabled {
		return EscalationDecision{false, "escalation disabled (opt-in off): the answer stays local"}
	}
	threshold := cfg.RepairThreshold
	if threshold <= 0 {
		threshold = 3
	}
	// Objective signals, in a fixed order so the reason is stable.
	reasons := []string{}
	if s.Abstained {
		reasons = append(reasons, "local model abstained")
	}
	if s.RepetitionGuardTripped {
		reasons = append(reasons, "repetition guard tripped")
	}
	if s.Repairs >= threshold {
		reasons = append(reasons, "repair storm")
	}
	if s.VerificationFailed {
		reasons = append(reasons, "verification failed")
	}
	if len(reasons) == 0 {
		if s.ModelUncertain {
			return EscalationDecision{false, "model self-declared uncertainty, but no objective signal fired: staying local"}
		}
		return EscalationDecision{false, "clean local turn: staying local"}
	}
	reason := reasons[0]
	for _, r := range reasons[1:] {
		reason += "; " + r
	}
	if s.ModelUncertain {
		reason += " (model also declared uncertainty)"
	}
	return EscalationDecision{true, reason}
}
