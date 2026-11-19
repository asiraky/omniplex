package codexapp

import (
	"os"
	"strconv"

	"github.com/asiraky/omniplex/internal/adapter"
)

// agentArgs is what an agent's session adds to codex's config: its compaction
// prompt, where it compacts, and no memories. skipped says why the prompt was
// left out, when it was.
//
// Codex reads the prompt file once, when the app-server loads its config, and
// fails to start at all if the file is missing; so an edit applies from the
// next process, not the next compaction, and a file that is not there yet is
// dropped rather than costing the session. The prompt replaces codex's own
// checkpoint prompt rather than adding to it, and codex ignores it when it
// compacts remotely (signed in to OpenAI). The limit is in tokens, and codex
// caps it at 90% of the model's window.
//
// Memories are off unless the user turned them on; the override wins over a
// config.toml that did, which an agent needs since its prompt is its memory.
func agentArgs(o adapter.CreateOptions) (args []string, skipped string) {
	if f := o.CompactInstructionsFile; f != "" {
		if _, err := os.Stat(f); err != nil {
			skipped = err.Error()
		} else {
			args = append(args, "-c", "experimental_compact_prompt_file="+strconv.Quote(f))
		}
	}
	if o.CompactAt > 0 {
		args = append(args, "-c", "model_auto_compact_token_limit="+strconv.FormatInt(o.CompactAt, 10))
	}
	if o.NoMemory {
		args = append(args, "-c", "features.memories=false")
	}
	return args, skipped
}
