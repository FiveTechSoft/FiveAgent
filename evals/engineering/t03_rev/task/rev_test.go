package task

import "testing"

func TestReverse(t *testing.T) {
	if Reverse("año") != "oña" || Reverse("ab") != "ba" {
		t.Fatal("wrong reverse")
	}
}
