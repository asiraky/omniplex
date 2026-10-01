package skills

import (
	"reflect"
	"strings"
	"testing"
)

const (
	manualLine = "disable-model-invocation: true\n"
	noImplicit = "policy:\n  allow_implicit_invocation: false\n"
)

func TestParseCodexSkills(t *testing.T) {
	config := `model = "gpt"
enabled = false

[[skills.config]] # first
path = "/a/SKILL.md" # trailing
enabled = false

[mcp_servers.x]
path = "/not/a/skill"
enabled = false

[[ skills.config ]]
path = '/b b/SKILL.md'

[[skills.config]]
enabled = false

[[skills.config]]
enabled = true # not false
path = "/c/\"q\"/SKILL.md"
`
	got := parseCodexSkills(strings.ReplaceAll(config, "\n", "\r\n"))
	want := []codexSkill{{"/a/SKILL.md", false}, {"/b b/SKILL.md", true}, {`/c/"q"/SKILL.md`, true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestEditFrontmatterManual(t *testing.T) {
	bom := string(rune(0xFEFF))
	folded := "---\nname: a\ndescription: >\n  mentions\n  disable-model-invocation: true\n  in passing\n---\nbody\n"
	tests := []struct {
		name   string
		in     string
		manual bool
		want   string
	}{
		{"absent, on: added as the last key", "---\nname: a\ndescription: d\n---\nbody\n", true, "---\nname: a\ndescription: d\ndisable-model-invocation: true\n---\nbody\n"},
		{"absent, off: untouched", "---\nname: a\n---\n", false, "---\nname: a\n---\n"},
		{"true, off: the line goes", "---\nname: a\ndisable-model-invocation: true\ndescription: d\n---\nbody\n", false, "---\nname: a\ndescription: d\n---\nbody\n"},
		{"true, on: untouched", "---\ndisable-model-invocation:   true # why\nname: a\n---\n", true, "---\ndisable-model-invocation:   true # why\nname: a\n---\n"},
		{"false, on: flipped where it stands", "---\ndisable-model-invocation:   false  # why\nname: a\n---\n", true, "---\ndisable-model-invocation:   true  # why\nname: a\n---\n"},
		{"false, off: untouched", "---\ndisable-model-invocation: false\nname: a\n---\n", false, "---\ndisable-model-invocation: false\nname: a\n---\n"},
		{"empty value, on", "---\ndisable-model-invocation:\nname: a\n---\n", true, "---\ndisable-model-invocation: true\nname: a\n---\n"},
		{"quoted key and value, off", "---\nname: a\n\"disable-model-invocation\": \"true\"\n---\n", false, "---\nname: a\n---\n"},
		{"quoted value, on", "---\ndisable-model-invocation: 'false'\n---\n", true, "---\ndisable-model-invocation: true\n---\n"},
		{"the text inside another key's value is not the key, off", folded, false, folded},
		{"the text inside another key's value is not the key, on", folded, true, strings.Replace(folded, "---\nbody", manualLine+"---\nbody", 1)},
		{"the same line in the body is not frontmatter", "---\nname: a\n---\ndisable-model-invocation: true\n", false, "---\nname: a\n---\ndisable-model-invocation: true\n"},
		{"other keys keep their quoting and comments", "---\nname: \"a\"  # quoted\nmetadata:\n  k: 'v'\nallowed-tools: [Read, \"Bash(git *)\"]\n---\n", true, "---\nname: \"a\"  # quoted\nmetadata:\n  k: 'v'\nallowed-tools: [Read, \"Bash(git *)\"]\ndisable-model-invocation: true\n---\n"},
		{"crlf, on", "---\r\nname: a\r\n---\r\nbody\r\n", true, "---\r\nname: a\r\ndisable-model-invocation: true\r\n---\r\nbody\r\n"},
		{"crlf, off", "---\r\nname: a\r\ndisable-model-invocation: true\r\ndescription: d\r\n---\r\n", false, "---\r\nname: a\r\ndescription: d\r\n---\r\n"},
		{"crlf, flipped", "---\r\ndisable-model-invocation: false\r\n---\r\n", true, "---\r\ndisable-model-invocation: true\r\n---\r\n"},
		{"byte order mark", bom + "---\nname: a\n---\n", true, bom + "---\nname: a\ndisable-model-invocation: true\n---\n"},
		{"closing line without a newline", "---\nname: a\n---", true, "---\nname: a\ndisable-model-invocation: true\n---"},
		{"no frontmatter, off: untouched", "# just a title\n", false, "# just a title\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := editFrontmatterManual(tt.in, tt.manual)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
			if _, _, err := parseFrontmatter(tt.in); err == nil && frontmatterManual(got) != tt.manual {
				t.Errorf("reads back as manual=%v", !tt.manual)
			}
		})
	}
	for _, in := range []string{"# just a title\n", "---\nname: a\n", ""} {
		if got, err := editFrontmatterManual(in, true); err == nil {
			t.Errorf("turning manual on in %q gave %q, want an error", in, got)
		}
	}
}

func TestEditOpenAIManual(t *testing.T) {
	nested := "policy:\n  nested:\n    allow_implicit_invocation: false\n"
	elsewhere := "interface:\n  policy:\n    allow_implicit_invocation: false\n"
	tests := []struct {
		name   string
		in     string
		manual bool
		want   string
	}{
		{"other keys stay, on", "interface:\n  display_name: \"S\"  # shown\npolicy:\n  allow_implicit_invocation: true\n  products: [a]\ndependencies: {}\n", true, "interface:\n  display_name: \"S\"  # shown\npolicy:\n  allow_implicit_invocation: false\n  products: [a]\ndependencies: {}\n"},
		{"other keys stay, off", "policy:\n    allow_implicit_invocation:  false # keep out\n    products: [a]\n", false, "policy:\n    allow_implicit_invocation:  true # keep out\n    products: [a]\n"},
		{"already manual", noImplicit, true, noImplicit},
		{"already allowed", "policy:\n  allow_implicit_invocation: true\n", false, "policy:\n  allow_implicit_invocation: true\n"},
		{"policy without the key, on: added at the siblings' indent", "policy:\n    products: [a]\ninterface: {}\n", true, "policy:\n    allow_implicit_invocation: false\n    products: [a]\ninterface: {}\n"},
		{"policy without the key, off: untouched", "policy:\n  products: [a]\n", false, "policy:\n  products: [a]\n"},
		{"empty policy, on", "policy:\ninterface: {}\n", true, "policy:\n  allow_implicit_invocation: false\ninterface: {}\n"},
		{"no policy, on: appended", "interface:\n  display_name: S\n", true, "interface:\n  display_name: S\n" + noImplicit},
		{"no policy, off: untouched", "interface:\n  display_name: S\n", false, "interface:\n  display_name: S\n"},
		{"no trailing newline", "interface: {}", true, "interface: {}\n" + noImplicit},
		{"policy on the last line without a newline", "policy:", true, "policy:\n  allow_implicit_invocation: false\n"},
		{"empty file", "", true, noImplicit},
		{"flow style, on", "policy: {products: [a], allow_implicit_invocation: true}\n", true, "policy: {products: [a], allow_implicit_invocation: false}\n"},
		{"flow style, off", "policy: {allow_implicit_invocation: false, products: [a]}\n", false, "policy: {allow_implicit_invocation: true, products: [a]}\n"},
		{"flow style without the key", "policy: {products: [a]}\n", true, "policy: {allow_implicit_invocation: false, products: [a]}\n"},
		{"empty flow", "policy: {}\n", true, "policy: {allow_implicit_invocation: false}\n"},
		{"a deeper key of the same name is not the policy", nested, true, "policy:\n  allow_implicit_invocation: false\n  nested:\n    allow_implicit_invocation: false\n"},
		{"a policy under another key is not the policy", elsewhere, true, elsewhere + noImplicit},
		{"crlf", "interface: {}\r\npolicy:\r\n  allow_implicit_invocation: true\r\n", true, "interface: {}\r\npolicy:\r\n  allow_implicit_invocation: false\r\n"},
		{"a comment at column 0 inside policy", "policy:\n# why\n  allow_implicit_invocation: true\n", true, "policy:\n# why\n  allow_implicit_invocation: false\n"},
		{"crlf, appended", "interface: {}\r\n", true, "interface: {}\r\npolicy:\r\n  allow_implicit_invocation: false\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := editOpenAIManual(tt.in, tt.manual)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
			if openaiManual(got) != tt.manual {
				t.Errorf("reads back as manual=%v", !tt.manual)
			}
		})
	}
	for _, in := range []string{nested, elsewhere} {
		if openaiManual(in) {
			t.Errorf("%q read as manual", in)
		}
	}
	if got, err := editOpenAIManual("policy: null\n", true); err == nil {
		t.Errorf("a policy that is not a map was edited to %q", got)
	}
}
