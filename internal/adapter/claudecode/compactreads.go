package claudecode

import "github.com/asiraky/omniplex/internal/adapter"

// claudeCompactHookCap is the most of a PreCompact hook's output Claude Code
// adds to its compaction prompt (see compactSettings).
const claudeCompactHookCap = 10_000

// CompactInstructions: the PreCompact hook cats the file each time Claude
// compacts, and Claude takes at most claudeCompactHookCap characters of it.
func (a *Adapter) CompactInstructions() adapter.CompactInstructionsUse {
	return adapter.CompactInstructionsUse{Reads: adapter.CompactReadsAtCompaction, MaxChars: claudeCompactHookCap}
}
