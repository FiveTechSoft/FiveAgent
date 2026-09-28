// guard.go - the repetition guard (stage 14 of docs/ROADMAP.md).
//
// A small model can degenerate into echoing one long fragment over and
// over. Delivering that echo to the user is worse than failing the
// turn: the guard detects a reply dominated by a single repeated
// fragment and aborts with a clear error (the channels turn it into
// their standard Spanish fallback line, and the log keeps the detail).
package agent

import "strings"

// repeatedFragment finds a tail fragment of s, at least minFrag bytes
// long, that repeats at least 3 times and covers at least half of s.
// It returns the fragment and its share of the reply; "" and 0 when the
// reply is healthy. Degenerate loops repeat at the end of the reply, so
// anchoring candidates at the tail keeps the check O(len(s)) per length.
func repeatedFragment(s string) (string, float64) {
	const minFrag = 30
	const maxFrag = 600
	n := len(s)
	maxL := n / 3
	if maxL > maxFrag {
		maxL = maxFrag
	}
	bestShare := 0.0
	best := ""
	for l := minFrag; l <= maxL; l++ {
		frag := s[n-l:]
		c := strings.Count(s, frag)
		if c >= 3 {
			if share := float64(c*l) / float64(n); share > bestShare {
				bestShare = share
				best = frag
			}
		}
	}
	if bestShare >= 0.5 {
		return best, bestShare
	}
	return "", 0
}
