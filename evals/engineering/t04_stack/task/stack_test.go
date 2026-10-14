package task

import "testing"

func TestDrainSum(t *testing.T) {
	s := &Stack{}
	s.Push(1)
	s.Push(2)
	s.Push(3)
	if got := DrainSum(s); got != 6 {
		t.Fatalf("DrainSum=%d want 6", got)
	}
	if s.Len() != 0 {
		t.Fatal("stack must be empty after draining")
	}
}
