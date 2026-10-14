package task

import "testing"

func TestAverage(t *testing.T) {
	if Average([]int{1, 2}) != 1.5 || Average(nil) != 0 {
		t.Fatal("wrong average")
	}
}
