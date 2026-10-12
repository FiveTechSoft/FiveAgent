package task

import "testing"

func TestSum(t *testing.T) {
	if Sum([]int{1, 2, 3}) != 6 || Sum(nil) != 0 {
		t.Fatal("wrong sum")
	}
}
