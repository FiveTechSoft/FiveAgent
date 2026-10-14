package task

// DrainSum pops everything and returns the total.
func DrainSum(s *Stack) int {
	t := 0
	for s.Len() > 0 {
		v, _ := s.Pop()
		t += v
	}
	return t
}
