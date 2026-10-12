package task

import "testing"

func TestMaxNegative(t *testing.T) {
	if m, ok := Max([]int{-5, -2, -9}); !ok || m != -2 {
		t.Fatalf("got %d %v", m, ok)
	}
}
