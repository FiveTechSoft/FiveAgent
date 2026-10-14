package task

import "testing"

func TestCount(t *testing.T) {
	m := Count("Go go  GO rust")
	if m["go"] != 3 || m["rust"] != 1 || len(m) != 2 {
		t.Fatalf("got %v", m)
	}
}
