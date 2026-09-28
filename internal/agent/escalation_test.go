package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// The seven claims made to the user, each proven by its own test.

// (1) A clean local turn never escalates: no cloud token is spent on
// what the local model handles well.
func TestEscalationCleanTurnStaysLocal(t *testing.T) {
	d := DecideEscalation(EscalationSignals{}, EscalationConfig{Enabled: true})
	if d.Escalate {
		t.Fatalf("clean turn must not escalate: %s", d.Reason)
	}
}

// (2) An abstention escalates: the model said "I don't know" instead
// of inventing, and that is exactly what the frontier is for.
func TestEscalationAbstentionEscalates(t *testing.T) {
	d := DecideEscalation(EscalationSignals{Abstained: true}, EscalationConfig{Enabled: true})
	if !d.Escalate {
		t.Fatal("abstention must escalate")
	}
}

// (3) A tripped repetition guard escalates.
func TestEscalationGuardTrippedEscalates(t *testing.T) {
	d := DecideEscalation(EscalationSignals{RepetitionGuardTripped: true}, EscalationConfig{Enabled: true})
	if !d.Escalate {
		t.Fatal("tripped guard must escalate")
	}
}

// (4) A repair storm escalates; repairs below the threshold do not.
func TestEscalationRepairStormEscalates(t *testing.T) {
	for _, n := range []int{0, 1, 2} {
		if d := DecideEscalation(EscalationSignals{Repairs: n}, EscalationConfig{Enabled: true}); d.Escalate {
			t.Fatalf("%d repairs must not escalate", n)
		}
	}
	for _, n := range []int{3, 4, 10} {
		if d := DecideEscalation(EscalationSignals{Repairs: n}, EscalationConfig{Enabled: true}); !d.Escalate {
			t.Fatalf("%d repairs must escalate (storm)", n)
		}
	}
}

// (5) The model cannot veto and cannot force: self-declared
// uncertainty alone, with every objective signal clean, never
// escalates; and a confident model with objective failure signals
// still escalates.
func TestEscalationModelCannotVetoOrForce(t *testing.T) {
	if d := DecideEscalation(EscalationSignals{ModelUncertain: true}, EscalationConfig{Enabled: true}); d.Escalate {
		t.Fatal("self-declared uncertainty alone must not escalate")
	}
	if d := DecideEscalation(EscalationSignals{Abstained: true}, EscalationConfig{Enabled: true}); !d.Escalate {
		t.Fatal("objective signal must escalate even without model uncertainty")
	}
}

// (6) Opt-in off: nothing escalates even when every signal fires.
func TestEscalationOptInOffNeverEscalates(t *testing.T) {
	all := EscalationSignals{
		Abstained:              true,
		RepetitionGuardTripped: true,
		Repairs:                99,
		VerificationFailed:     true,
		ModelUncertain:         true,
	}
	if d := DecideEscalation(all, EscalationConfig{Enabled: false}); d.Escalate {
		t.Fatal("with opt-in off nothing may ever escalate")
	}
}

// (7) The decision is deterministic: same inputs, same verdict, and
// the reason string is stable.
func TestEscalationDeterministic(t *testing.T) {
	s := EscalationSignals{Abstained: true, Repairs: 5, ModelUncertain: true}
	d1 := DecideEscalation(s, EscalationConfig{Enabled: true})
	d2 := DecideEscalation(s, EscalationConfig{Enabled: true})
	if d1 != d2 {
		t.Fatalf("non-deterministic: %+v vs %+v", d1, d2)
	}
}

// The exhaustive decision table: every combination of the five binary
// signals, repairs 0..4 around the threshold, and opt-in on/off. The
// expected verdict is derived independently right here, so the test
// is the specification.
func TestEscalationDecisionTableExhaustive(t *testing.T) {
	cases := 0
	for _, enabled := range []bool{false, true} {
		for _, abstained := range []bool{false, true} {
			for _, guard := range []bool{false, true} {
				for _, verif := range []bool{false, true} {
					for _, uncertain := range []bool{false, true} {
						for repairs := 0; repairs <= 4; repairs++ {
							s := EscalationSignals{
								Abstained:              abstained,
								RepetitionGuardTripped: guard,
								Repairs:                repairs,
								VerificationFailed:     verif,
								ModelUncertain:         uncertain,
							}
							want := enabled && (abstained || guard || verif || repairs >= 3)
							if d := DecideEscalation(s, EscalationConfig{Enabled: enabled}); d.Escalate != want {
								t.Errorf("signals %+v enabled=%v: got %v, want %v (%s)", s, enabled, d.Escalate, want, d.Reason)
							}
							if d := DecideEscalation(s, EscalationConfig{Enabled: false}); d.Escalate {
								t.Errorf("opt-in off must never escalate: signals %+v", s)
							}
							cases++
						}
					}
				}
			}
		}
	}
	t.Logf("exhaustive table: %d combinations verified", cases)
}

// The no-path guarantee, verified from source: the decision module
// imports nothing, so it structurally cannot reach a network, a disk,
// or any provider. If anyone ever adds an import to escalation.go,
// this test turns red.
func TestEscalationModuleIsPure(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(file), "escalation.go"), nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse escalation.go: %v", err)
	}
	for _, imp := range f.Imports {
		if imp.Path.Value != "" {
			ast.Print(fset, imp)
			t.Errorf("escalation.go must import nothing; found %s", imp.Path.Value)
		}
	}
}
