package memory

import (
	"regexp"
	"strings"
)

// redactedPlaceholder replaces a word the user asked to forget. It is
// deliberately meaningless: the model can see that something was there
// and is gone, but cannot quote it, and the battery's must_not_contain
// tokens are not present in the history any more.
const redactedPlaceholder = "[olvidado]"

// RedactWords replaces every case-insensitive occurrence of each word
// in s with the placeholder and reports whether anything changed.
// Words are matched literally - a query word with regex metacharacters
// must not turn into a pattern.
func RedactWords(s string, words []string) (string, bool) {
	out := s
	changed := false
	for _, w := range words {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		re, err := regexp.Compile(`(?i)` + regexp.QuoteMeta(w))
		if err != nil {
			continue
		}
		if next := re.ReplaceAllString(out, redactedPlaceholder); next != out {
			out = next
			changed = true
		}
	}
	return out, changed
}

// ContentTokens returns the query's content words (three letters or
// more, no stopwords, no repeats) - the words a forget command is
// really about. The agent uses them to redact the stored history.
func ContentTokens(match string) []string {
	return forgetTokens(match)
}
