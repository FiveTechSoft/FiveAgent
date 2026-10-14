package task

// Stack is a LIFO of ints.
type Stack struct{ items []int }

func (s *Stack) Push(v int) { s.items = append(s.items, v) }

// Pop removes and returns the top; ok is false when empty.
func (s *Stack) Pop() (int, bool) {
	if len(s.items) == 0 {
		return 0, false
	}
	v := s.items[len(s.items)-1]
	return v, true
}

func (s *Stack) Len() int { return len(s.items) }
