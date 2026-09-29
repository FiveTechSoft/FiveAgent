// Package skills embeds the on-disk skill library (one SKILL.md per
// skill, stage 32 of docs/ROADMAP.md) so the binary works when the
// working directory has no skills/ folder. On-disk files win at
// runtime; these copies are the build-time fallback.
package skills

import "embed"

//go:embed all:fivetech
var FS embed.FS
