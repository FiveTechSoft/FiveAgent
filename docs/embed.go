// Package docs embeds the human-maintained FiveTech domain reference so
// the binary can inject it into the system prompt on every turn. The
// markdown file stays the single source of truth: edit the .md, rebuild,
// and the agent answers with the verified facts.
package docs

import _ "embed"

// FiveTechDomain is the content of fivetech-domain.md: verified facts
// about the FiveTech ecosystem (FiveWin, Harbour, FWH) that small models
// confabulate when left to their general knowledge. Injected into the
// system prompt by agent.SystemPrompt.
//
//go:embed fivetech-domain.md
var FiveTechDomain string
