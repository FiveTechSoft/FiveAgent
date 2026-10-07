package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// effectTools is the fixed list of tools whose call changes something
// outside the conversation: messages, calendar, issues, uploads, files,
// commands, the shared browser and scheduled jobs. With effect
// confirmation on, the first call only records a pending approval and
// tells the model to ask the user. The identical call runs after the
// user's next message is a plain yes. The list is deliberately explicit:
// a new tool stays unconfirmed until someone reviews and adds it here.
var effectTools = map[string]bool{
	"gmail_send": true, "slack_send": true, "calendar_create": true,
	"github_create_issue": true, "drive_upload": true, "send_chart": true,
	"run_command": true, "browser_act": true, "write_file": true,
	"edit_file": true, "schedule_job": true,
}

const consentTTL = 10 * time.Minute

// WithEffectConfirmation turns on explicit confirmation for effectTools.
// It is off by default so existing callers and mock-based tests keep
// their behavior; the application enables it.
func (a *Agent) WithEffectConfirmation() *Agent {
	a.confirmEffects = true
	return a
}

func callHash(name string, args []byte) string {
	norm := args
	var v any
	if json.Unmarshal(args, &v) == nil {
		if b, err := json.Marshal(v); err == nil { // sorted keys, no spacing
			norm = b
		}
	}
	sum := sha256.Sum256(append([]byte(name+"\x00"), norm...))
	return hex.EncodeToString(sum[:])
}

var affirmations = map[string]bool{
	"si": true, "sí": true, "ok": true, "vale": true, "confirmo": true,
	"adelante": true, "dale": true, "hazlo": true, "yes": true, "y": true,
	"confirmado": true, "de acuerdo": true, "sí, hazlo": true, "si, hazlo": true,
}

func isAffirmation(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.Trim(t, " .!¡¿?")
	return affirmations[t]
}

// takeApprovals returns the calls the user just approved for this
// sender and clears every pending entry: an approval covers only the
// message right after the request, never a later unrelated turn.
func (a *Agent) takeApprovals(user, text string) map[string]bool {
	a.consentMu.Lock()
	defer a.consentMu.Unlock()
	pend := a.pending[user]
	delete(a.pending, user)
	if !isAffirmation(text) {
		return nil
	}
	ok := map[string]bool{}
	for h, at := range pend {
		if time.Since(at) <= consentTTL {
			ok[h] = true
		}
	}
	return ok
}

func (a *Agent) addPending(user, h string) {
	a.consentMu.Lock()
	defer a.consentMu.Unlock()
	if a.pending == nil {
		a.pending = map[string]map[string]time.Time{}
	}
	if a.pending[user] == nil {
		a.pending[user] = map[string]time.Time{}
	}
	a.pending[user][h] = time.Now()
}

// gateEffect decides whether a call may run. It returns the text to feed
// back to the model when the call is held.
func (a *Agent) gateEffect(user, name string, args []byte, approved map[string]bool) (string, bool) {
	if !a.confirmEffects || !effectTools[name] {
		return "", true
	}
	h := callHash(name, args)
	if approved[h] {
		delete(approved, h) // one approval, one execution
		return "", true
	}
	a.addPending(user, h)
	return fmt.Sprintf("confirmation_required: %s was NOT executed. Tell the user exactly what it would do (recipient, content, target) and ask them to reply yes. Only after their yes, call %s again with the same arguments.", name, name), false
}
