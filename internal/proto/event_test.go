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

func TestSuggested(t *testing.T) {
	long := ""
	for range MaxSuggestion + 50 {
		long += "é"
	}
	for _, tc := range []struct {
		name, turn, in, want string
		ok                   bool
	}{
		{"trimmed", "t1", "  run the tests \n", "run the tests", true},
		{"one line", "t1", "run the tests\nthen commit", "run the tests then commit", true},
		{"empty", "t1", " \n\t ", "", false},
		{"no turn", "", "run the tests", "", false},
		{"capped in runes", "t1", long, long[:MaxSuggestion*len("é")], true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := Suggested(tc.turn, tc.in)
			if ok != tc.ok || p.Text != tc.want {
				t.Fatalf("Suggested(%q, %q) = %q, %v; want %q, %v", tc.turn, tc.in, p.Text, ok, tc.want, tc.ok)
			}
			if ok && p.TurnID != tc.turn {
				t.Fatalf("turn id %q, want %q", p.TurnID, tc.turn)
			}
		})
	}
}
