package proto

import "testing"

func TestAttachmentTitle(t *testing.T) {
	img := PromptImage{MediaType: "image/png"}
	pdf := PromptImage{MediaType: "application/pdf"}
	for _, tc := range []struct {
		in   []PromptImage
		want string
	}{
		{[]PromptImage{img}, "1 image"},
		{[]PromptImage{img, img}, "2 images"},
		{[]PromptImage{pdf}, "1 PDF"},
		{[]PromptImage{pdf, pdf, pdf}, "3 PDFs"},
		{[]PromptImage{img, pdf}, "2 attachments"},
	} {
		if got := AttachmentTitle(tc.in); got != tc.want {
			t.Errorf("AttachmentTitle(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
