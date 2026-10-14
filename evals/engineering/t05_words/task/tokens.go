package task

import "strings"

// Tokens splits text into lowercase words separated by spaces.
func Tokens(s string) []string {
	return strings.Split(s, " ")
}
