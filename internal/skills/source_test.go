package skills

import (
	"reflect"
	"testing"
)

func TestParseSource(t *testing.T) {
	const home = "/home/me"
	tests := []struct {
		name  string
		input string
		want  ParsedSource
	}{
		{"owner/repo", "humanlayer/skills", ParsedSource{Repo: "humanlayer/skills"}},
		{"surrounding space", "  humanlayer/skills\n", ParsedSource{Repo: "humanlayer/skills"}},
		{"owner/repo at a ref", "humanlayer/skills#v2", ParsedSource{Repo: "humanlayer/skills", Ref: "v2"}},
		{"a ref with a slash", "owner/repo#feature/new-skill", ParsedSource{Repo: "owner/repo", Ref: "feature/new-skill"}},
		{"owner/repo naming a skill", "owner/repo@show-me", ParsedSource{Repo: "owner/repo", Picked: []string{"show-me"}}},
		{"a repo name with dots", "owner/repo.js", ParsedSource{Repo: "owner/repo.js"}},

		{"a whole pasted command", "npx skills add humanlayer/skills --skill show-me -g -a claude-code -y",
			ParsedSource{Repo: "humanlayer/skills", Picked: []string{"show-me"}}},
		{"a versioned CLI and several skills", "npx -y skills@1.7.0 add vercel-labs/agent-skills -s one two --copy",
			ParsedSource{Repo: "vercel-labs/agent-skills", Picked: []string{"one", "two"}}},
		{"every skill, quoted for the shell", "npx skills add owner/repo --skill '*'",
			ParsedSource{Repo: "owner/repo", Picked: []string{"*"}}},
		{"flags before the source", "npx skills add -g -y owner/repo --skill=one",
			ParsedSource{Repo: "owner/repo", Picked: []string{"one"}}},
		{"agents are not skills", "npx skills add owner/repo -a claude-code cursor -s one",
			ParsedSource{Repo: "owner/repo", Picked: []string{"one"}}},
		{"no skill named", "npx skills add owner/repo#main -y", ParsedSource{Repo: "owner/repo", Ref: "main"}},
		{"another runner", "bunx skills add owner/repo", ParsedSource{Repo: "owner/repo"}},
		{"a copied shell prompt", "$ npx skills add owner/repo", ParsedSource{Repo: "owner/repo"}},
		{"a command with a URL", `npx skills add "https://github.com/owner/repo" --skill one`,
			ParsedSource{Repo: "owner/repo", Picked: []string{"one"}}},
		{"a command with a folder", "npx skills add ~/code/skills -s one",
			ParsedSource{Local: true, Repo: "/home/me/code/skills", Picked: []string{"one"}}},

		{"a github URL", "https://github.com/owner/repo", ParsedSource{Repo: "owner/repo"}},
		{"a github clone URL", "https://github.com/owner/repo.git", ParsedSource{Repo: "owner/repo"}},
		{"a github URL with a trailing slash", "https://www.github.com/owner/repo/", ParsedSource{Repo: "owner/repo"}},
		{"a github tree URL", "https://github.com/owner/repo/tree/main/skills/show-me",
			ParsedSource{Repo: "owner/repo", Ref: "main", Path: "skills/show-me"}},
		{"a github tree URL at the top", "https://github.com/owner/repo/tree/v1.2", ParsedSource{Repo: "owner/repo", Ref: "v1.2"}},
		{"a github link to a SKILL.md", "https://github.com/owner/repo/blob/main/skills/show-me/SKILL.md?plain=1",
			ParsedSource{Repo: "owner/repo", Ref: "main", Path: "skills/show-me"}},
		{"a git@ address", "git@github.com:owner/repo.git", ParsedSource{Repo: "git@github.com:owner/repo.git"}},
		{"a git@ address at a ref", "git@gitlab.example.com:group/sub/repo.git#dev",
			ParsedSource{Repo: "git@gitlab.example.com:group/sub/repo.git", Ref: "dev"}},
		{"another host", "https://gitlab.com/group/sub/repo.git#v3", ParsedSource{Repo: "https://gitlab.com/group/sub/repo.git", Ref: "v3"}},
		{"an ssh URL", "ssh://git@example.com/group/repo.git", ParsedSource{Repo: "ssh://git@example.com/group/repo.git"}},

		{"a folder under home", "~/code/skills", ParsedSource{Local: true, Repo: "/home/me/code/skills"}},
		{"home itself", "~", ParsedSource{Local: true, Repo: "/home/me"}},
		{"an absolute folder", "/srv/skills/", ParsedSource{Local: true, Repo: "/srv/skills"}},
		{"a folder with a space", "/srv/my skills", ParsedSource{Local: true, Repo: "/srv/my skills"}},
		{"a folder that is not tidy", "/srv/a/../skills", ParsedSource{Local: true, Repo: "/srv/skills"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSource(tt.input, home)
			if err != nil {
				t.Fatalf("ParseSource(%q): %v", tt.input, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseSource(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}

	garbage := map[string]string{
		"nothing":                         "",
		"only space":                      "  \n ",
		"a sentence":                      "please install the skills",
		"one bare word":                   "skills",
		"a relative folder":               "./skills",
		"a folder climbing out":           "../skills",
		"three path segments":             "owner/repo/extra",
		"a command with no source":        "npx skills add -g -y",
		"another skills command":          "npx skills update owner/repo",
		"two sources":                     "npx skills add owner/repo other/repo",
		"an unfinished quote":             "npx skills add owner/repo --skill 'one",
		"a flag as the source":            "--upload-pack=touch${IFS}pwned",
		"a flag as the owner":             "-o/repo",
		"a flag as the ref":               "owner/repo#--force",
		"a ref with a space":              "owner/repo#a b",
		"a git remote helper":             "ext::sh",
		"a file URL":                      "file:///etc",
		"a token in the URL":              "https://ghp_secret@github.com/owner/repo",
		"a password in an ssh URL":        "ssh://git:secret@example.com/repo.git",
		"a URL with no repo":              "https://example.com",
		"a github user page":              "https://github.com/owner",
		"a github page that is no folder": "https://github.com/owner/repo/issues/3",
		"a tree URL climbing out":         "https://github.com/owner/repo/tree/main/../../x",
		"a skill name with nothing in it": "owner/repo@",
		"a folder under no home":          "~/skills",
	}
	for name, input := range garbage {
		t.Run(name, func(t *testing.T) {
			h := home
			if name == "a folder under no home" {
				h = ""
			}
			if got, err := ParseSource(input, h); err == nil {
				t.Errorf("ParseSource(%q) = %+v, want an error", input, got)
			}
		})
	}
}

func TestWhatEachFetcherIsHanded(t *testing.T) {
	tests := []struct {
		input       string
		spec, clone string
	}{
		{"owner/repo", "owner/repo", "https://github.com/owner/repo.git"},
		// The ref goes to the CLI as the user wrote it, never resolved to a commit.
		{"owner/repo#main", "owner/repo#main", "https://github.com/owner/repo.git"},
		{"https://github.com/owner/repo/tree/v2/skills", "owner/repo#v2", "https://github.com/owner/repo.git"},
		{"git@example.com:group/repo.git#dev", "git@example.com:group/repo.git#dev", "git@example.com:group/repo.git"},
		{"https://gitlab.com/group/repo", "https://gitlab.com/group/repo", "https://gitlab.com/group/repo"},
	}
	for _, tt := range tests {
		p, err := ParseSource(tt.input, "/home/me")
		if err != nil {
			t.Fatalf("%q: %v", tt.input, err)
		}
		if p.spec() != tt.spec || p.cloneURL() != tt.clone {
			t.Errorf("%q: npx gets %q and git %q, want %q and %q", tt.input, p.spec(), p.cloneURL(), tt.spec, tt.clone)
		}
	}
}
