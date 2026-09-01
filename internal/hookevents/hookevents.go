// Package hookevents holds the canonical names of Angela's hook events.
//
// The package exists because internal/hooks already depends on
// internal/config (to read hook definitions from it), so config cannot
// import hooks back without a cycle. Keeping this package dependency-free
// lets both sides import it instead of one of them re-declaring the
// literal, mirroring internal/toolnames.
package hookevents

// PreToolUse fires before a tool call executes; a hook can deny or allow it.
const PreToolUse = "PreToolUse"
