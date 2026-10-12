package task

// Sum returns the sum of all numbers.
func Sum(n []int) int {
	t := 0
	for i := 1; i < len(n); i++ {
		t += n[i]
	}
	return t
}
