package task

// Max returns the largest element; ok is false for an empty slice.
func Max(n []int) (int, bool) {
	if len(n) == 0 {
		return 0, false
	}
	m := 0
	for _, v := range n {
		if v > m {
			m = v
		}
	}
	return m, true
}
