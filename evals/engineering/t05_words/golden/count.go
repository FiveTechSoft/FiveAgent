package task

// Count returns how many times each word appears.
func Count(s string) map[string]int {
	m := map[string]int{}
	for _, w := range Tokens(s) {
		m[w]++
	}
	return m
}
