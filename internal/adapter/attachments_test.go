package adapter

import (
	"testing"

	"github.com/asiraky/omniplex/internal/proto"
)

func TestSplitAttachmentsKeepsOrderWithinEachKind(t *testing.T) {
	all := []proto.PromptImage{
		{ID: "a", MediaType: "image/png"},
		{ID: "b", MediaType: "application/pdf"},
		{ID: "c", MediaType: "image/jpeg"},
		{ID: "d", MediaType: "application/pdf"},
	}
	images, docs := SplitAttachments(all)
	if len(images) != 2 || images[0].ID != "a" || images[1].ID != "c" {
		t.Fatalf("images = %+v", images)
	}
	if len(docs) != 2 || docs[0].ID != "b" || docs[1].ID != "d" {
		t.Fatalf("documents = %+v", docs)
	}
}

func TestWithDocumentPaths(t *testing.T) {
	one := []proto.PromptImage{{Path: "/s/a.pdf"}}
	two := []proto.PromptImage{{Path: "/s/a.pdf"}, {Path: "/s/b.pdf"}}
	for _, tc := range []struct {
		name string
		text string
		docs []proto.PromptImage
		want string
	}{
		{"no documents leaves the text alone", "hi", nil, "hi"},
		{"one document leads the text", "summarise", one, "[Attached PDF: /s/a.pdf]\n\nsummarise"},
		{"a document alone sends no empty text", "", one, "[Attached PDF: /s/a.pdf]"},
		{"several are listed", "compare", two, "[Attached PDFs:\n- /s/a.pdf\n- /s/b.pdf]\n\ncompare"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WithDocumentPaths(tc.text, tc.docs); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
