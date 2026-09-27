package adapter

import (
	"strings"

	"github.com/asiraky/omniplex/internal/proto"
)

// SplitAttachments separates what a prompt carried into the images a harness
// takes as pictures and the documents (PDFs) it has to be handed some other
// way. Order within each is kept.
func SplitAttachments(all []proto.PromptImage) (images, documents []proto.PromptImage) {
	for _, a := range all {
		if strings.HasPrefix(a.MediaType, "image/") {
			images = append(images, a)
		} else {
			documents = append(documents, a)
		}
	}
	return images, documents
}

// WithDocumentPaths hands documents to a harness that has no native way to take
// them: their paths lead the prompt, and the agent reads them with its own
// tools. The paths are absolute, because the harness runs in the session's
// checkout and the files live in the server's attachment store.
func WithDocumentPaths(text string, documents []proto.PromptImage) string {
	if len(documents) == 0 {
		return text
	}
	var b strings.Builder
	if len(documents) == 1 {
		b.WriteString("[Attached PDF: " + documents[0].Path + "]")
	} else {
		b.WriteString("[Attached PDFs:")
		for _, d := range documents {
			b.WriteString("\n- " + d.Path)
		}
		b.WriteString("]")
	}
	if text != "" {
		b.WriteString("\n\n" + text)
	}
	return b.String()
}
