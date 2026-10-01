package skills

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// ParsedSource is what the user pasted into the install box, taken apart.
type ParsedSource struct {
	// Local is a folder on this machine; everything else is fetched.
	Local bool
	// Repo is "owner/repo" for GitHub, the URL for any other host, and the
	// absolute folder when Local. It is what the source record keeps.
	Repo string
	Ref  string
	// Path is the folder inside the repo a tree URL pointed at,
	// slash-separated. Only skills under it are offered.
	Path string
	// Picked are the skills a pasted command named with --skill or -s.
	// "*" is all of them.
	Picked []string
}

var (
	shorthandRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	scpRe       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9_.~/-]+$`)
	// A ref becomes an argument to git and to the skills CLI, so it may not
	// look like a flag.
	refRe     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./+@-]*$`)
	fullShaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

var errNotASource = errors.New("give owner/repo, a git URL, a folder starting with / or ~/, or an npx skills add command")

// ParseSource reads the install box: a repo, a URL, a folder, or a whole
// `npx skills add` line copied from a README. home expands a leading ~.
func ParseSource(input, home string) (ParsedSource, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return ParsedSource{}, errNotASource
	}
	// A folder may have spaces in it, so it is taken whole rather than split
	// into words.
	if isFolder(input) {
		return parseFolder(input, home)
	}
	words, err := splitWords(input)
	if err != nil {
		return ParsedSource{}, err
	}
	// A line copied out of a terminal brings its prompt along.
	if len(words) > 1 && words[0] == "$" {
		words = words[1:]
	}
	if args, ok := addCommand(words); ok {
		return parseAdd(args, home)
	}
	if len(words) != 1 {
		return ParsedSource{}, errNotASource
	}
	return parseOne(words[0], home)
}

func isFolder(s string) bool {
	return s == "~" || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "/")
}

func parseFolder(s, home string) (ParsedSource, error) {
	if s == "~" || strings.HasPrefix(s, "~/") {
		if home == "" {
			return ParsedSource{}, errors.New("no home folder to expand ~ in")
		}
		s = filepath.Join(home, filepath.FromSlash(s[1:]))
	}
	return ParsedSource{Local: true, Repo: filepath.Clean(s)}, nil
}

// splitWords splits a command line the way a shell would, as far as quotes
// and backslashes go. Nothing is expanded.
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	in := false // a word is open, possibly an empty quoted one
	quote := rune(0)
	escaped := false
	for _, c := range s {
		switch {
		case escaped:
			cur.WriteRune(c)
			escaped = false
		case quote == '\'':
			if c == '\'' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\\':
			escaped, in = true, true
		case quote == '"':
			if c == '"' {
				quote = 0
			} else {
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote, in = c, true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(c)
			in = true
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("unfinished quote in the command")
	}
	if in {
		words = append(words, cur.String())
	}
	return words, nil
}

// addCommand finds `skills add` in a command line, however it was launched
// (npx, bunx, pnpm dlx, with or without a version), and returns what follows.
func addCommand(words []string) ([]string, bool) {
	for i := 0; i+1 < len(words); i++ {
		if words[i] != "skills" && !strings.HasPrefix(words[i], "skills@") {
			continue
		}
		switch words[i+1] {
		case "add", "a", "install", "i":
			return words[i+2:], true
		}
		return nil, false
	}
	return nil, false
}

// parseAdd reads the arguments of `skills add`. Only the source and the
// skills it names matter here: where the CLI would have installed them, and
// for which agents, is decided in the install dialog instead.
func parseAdd(args []string, home string) (ParsedSource, error) {
	var source string
	var picked []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--skill" || a == "-s" || a == "--agent" || a == "-a":
			// These take every word up to the next flag, as the CLI reads them.
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				if a == "--skill" || a == "-s" {
					picked = append(picked, args[i])
				}
			}
		case strings.HasPrefix(a, "--skill="):
			picked = append(picked, strings.TrimPrefix(a, "--skill="))
		case strings.HasPrefix(a, "-"):
		case source == "":
			source = a
		default:
			return ParsedSource{}, fmt.Errorf("unexpected %q after the source", a)
		}
	}
	if source == "" {
		return ParsedSource{}, errors.New("the command names no source")
	}
	p, err := parseOne(source, home)
	if err != nil {
		return ParsedSource{}, err
	}
	p.Picked = append(p.Picked, picked...)
	return p, nil
}

// parseOne reads a single source word.
func parseOne(s, home string) (ParsedSource, error) {
	if isFolder(s) {
		return parseFolder(s, home)
	}
	s, ref, _ := strings.Cut(s, "#")
	p, err := parseRemote(s)
	if err != nil {
		return ParsedSource{}, err
	}
	if p.Ref == "" {
		p.Ref = ref
	}
	if p.Ref != "" && !refRe.MatchString(p.Ref) {
		return ParsedSource{}, fmt.Errorf("%q is not a branch, tag or commit", p.Ref)
	}
	return p, nil
}

// parseRemote reads a repo with no #ref on it: owner/repo, owner/repo@skill,
// a git@ address or a URL.
func parseRemote(s string) (ParsedSource, error) {
	switch {
	case s == "" || strings.HasPrefix(s, "-"):
		return ParsedSource{}, errNotASource
	case scpRe.MatchString(s):
		return ParsedSource{Repo: s}, nil
	case strings.Contains(s, "://"):
		return parseURL(s)
	}
	repo, skill, named := strings.Cut(s, "@")
	if !shorthandRe.MatchString(repo) {
		return ParsedSource{}, errNotASource
	}
	p := ParsedSource{Repo: strings.TrimSuffix(repo, ".git")}
	if named {
		if skill == "" {
			return ParsedSource{}, errNotASource
		}
		p.Picked = []string{skill}
	}
	return p, nil
}

func parseURL(s string) (ParsedSource, error) {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return ParsedSource{}, errNotASource
	}
	switch u.Scheme {
	case "https", "http":
		// The repo is written into the source record, which travels with the
		// library; a token in the URL would travel with it.
		if u.User != nil {
			return ParsedSource{}, errors.New("leave the username and token out of the URL")
		}
	case "ssh":
		if _, has := u.User.Password(); has {
			return ParsedSource{}, errors.New("leave the password out of the URL")
		}
	default:
		return ParsedSource{}, fmt.Errorf("cannot fetch from a %s URL", u.Scheme)
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	if host != "github.com" || u.Scheme == "ssh" {
		if u.Path == "" {
			return ParsedSource{}, errNotASource
		}
		return ParsedSource{Repo: u.String()}, nil
	}

	// GitHub is written owner/repo whichever way it was pasted, so one repo is
	// one source whether it came from a README, the address bar, or the
	// skills CLI's own lock.
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ParsedSource{}, errNotASource
	}
	repo := parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
	if !shorthandRe.MatchString(repo) {
		return ParsedSource{}, errNotASource
	}
	p := ParsedSource{Repo: repo}
	rest := parts[2:]
	switch {
	case len(rest) == 0:
		return p, nil
	case len(rest) >= 2 && (rest[0] == "tree" || rest[0] == "blob"):
		p.Ref = rest[1]
		sub := path.Join(rest[2:]...)
		// A link to a file is a link to the folder it is in.
		if rest[0] == "blob" {
			sub = path.Dir(sub)
		}
		if sub == "." {
			sub = ""
		}
		if sub != "" && !filepath.IsLocal(filepath.FromSlash(sub)) {
			return ParsedSource{}, errNotASource
		}
		p.Path = sub
		return p, nil
	}
	return ParsedSource{}, errors.New("that GitHub page is not a repository or a folder in one")
}

// spec is the source as the skills CLI takes it.
func (p ParsedSource) spec() string {
	if p.Ref == "" {
		return p.Repo
	}
	return p.Repo + "#" + p.Ref
}

// cloneURL is the source as git takes it.
func (p ParsedSource) cloneURL() string {
	if shorthandRe.MatchString(p.Repo) {
		return "https://github.com/" + p.Repo + ".git"
	}
	return p.Repo
}

// shortName is the last part of the repo, the name a skill sitting at the top
// of it falls back to.
func (p ParsedSource) shortName() string {
	name := strings.TrimSuffix(strings.TrimRight(filepath.ToSlash(p.Repo), "/"), ".git")
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		name = name[i+1:]
	}
	return name
}
