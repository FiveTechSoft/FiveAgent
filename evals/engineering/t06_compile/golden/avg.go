package task

// Average returns the mean of the numbers, or 0 for none.
func Average(n []int) float64 {
	if len(n) == 0 {
		return 0
	}
	s := 0
	for _, v := range n {
		s += v
	}
	return float64(s) / float64(len(n))
}
