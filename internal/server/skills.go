package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/thread"
)

func (s *Server) skillCommand(ctx context.Context, command string, a skillArgs) (any, error) {
	roots, projectName, err := s.mgr.SkillRoots(ctx, a.ThreadID, a.ProjectID)
	if err != nil {
		return nil, err
	}
	switch command {
	case "set_skill_mode":
		return marked(ctx, roots)(skills.SetMode(roots, a.Dir, a.Mode))
	case "remove_skill":
		if err := skills.Remove(roots, a.Dir); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "list_skills":
		found, err := skills.Discover(roots)
		if err != nil {
			return nil, err
		}
		skills.MarkUncommitted(ctx, roots, found)
		out := map[string]any{
			"skills": found, "claudeSync": skills.ClaudeSync(roots), "codexBundled": skills.CodexBundled(roots),
			"claudeBundled":  skills.ClaudeBundled(roots),
			"claudeBuiltins": skills.ClaudeBuiltins(roots, s.claudeBundled.Names(), found),
			"destinations":   roots.Destinations(), "defaultDestination": roots.DefaultDestination(),
		}
		if roots.ProjectRoot != "" {
			out["projectRoot"], out["projectName"] = roots.ProjectRoot, projectName
		}
		return out, nil
	case "set_claude_sync":
		if err := skills.SetClaudeSync(roots, a.On); err != nil {
			return nil, err
		}
		return map[string]any{"claudeSync": skills.ClaudeSync(roots)}, nil
	case "set_claude_bundled":
		if err := skills.SetClaudeBundled(roots, a.On); err != nil {
			return nil, err
		}
		return map[string]any{"claudeBundled": skills.ClaudeBundled(roots)}, nil
	case "set_claude_builtin":
		return skills.SetClaudeBuiltin(roots, a.Name, a.On)
	case "set_codex_bundled":
		if err := skills.SetCodexBundled(roots, a.On); err != nil {
			return nil, err
		}
		return map[string]any{"codexBundled": skills.CodexBundled(roots)}, nil
	case "read_skill":
		return skills.Read(roots, a.Dir)
	case "read_skill_file":
		content, binary, err := skills.ReadFile(roots, a.Dir, a.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": content, "binary": binary}, nil
	case "save_skill":
		if err := skills.Save(roots, a.Dir, a.Content); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "create_skill":
		return marked(ctx, roots)(skills.Create(roots, a.Name, a.Description, a.Destination))

	case "stage_skills":
		return s.skillFetch.Stage(ctx, roots, a.Source)
	case "read_staged_file":
		content, binary, err := skills.ReadStagedFile(a.ID, a.Skill, a.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": content, "binary": binary}, nil
	case "install_staged":
		placed, err := skills.InstallStaged(roots, a.ID, a.Skills, a.Destination)
		if err != nil {
			return nil, err
		}
		skills.MarkUncommitted(ctx, roots, placed)
		return map[string]any{"skills": placed}, nil
	case "discard_staged":
		if err := skills.DiscardStaged(a.ID); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil

	case "skills_git_status":
		status, err := skills.LibraryStatus(ctx, roots)
		if err != nil {
			return nil, err
		}
		return map[string]any{"git": status}, nil
	case "commit_skills":
		commit, status, err := skills.CommitSkills(ctx, roots, a.Names, a.Message)
		if err != nil {
			return nil, err
		}
		return map[string]any{"commit": commit, "git": status}, nil

	case "stage_update":
		return s.skillFetch.StageUpdate(ctx, roots, a.Dir)
	case "read_update_file":
		before, after, binary, err := skills.ReadUpdateFile(roots, a.ID, a.Dir, a.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"old": before, "new": after, "binary": binary}, nil
	case "apply_update":
		updated, err := skills.ApplyUpdate(roots, a.ID, a.Dirs)
		if err != nil {
			return nil, err
		}
		skills.MarkUncommitted(ctx, roots, updated)
		return map[string]any{"skills": updated}, nil
	}
	return nil, fmt.Errorf("unknown skill command %q", command)
}

// marked passes on a skill a command just wrote, marked when that left it
// uncommitted in a main checkout.
func marked(ctx context.Context, roots skills.Roots) func(skills.Skill, error) (any, error) {
	return func(s skills.Skill, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		one := []skills.Skill{s}
		skills.MarkUncommitted(ctx, roots, one)
		return one[0], nil
	}
}

// attachedFiles is the trailer a prompt carries for the files a human
// attached: what each is and where it is on this host, so the agent can read
// it. The web client parses the same block back out to draw the files as
// cards on the message.
func (s *Server) attachedFiles(ctx context.Context, actor *thread.Actor, files []promptFile) (string, error) {
	var b strings.Builder
	b.WriteString("\n\n<attached-files>\n")
	for _, f := range files {
		a, err := actor.Artefact(ctx, f.ArtefactID)
		if err != nil {
			return "", fmt.Errorf("no such file %s", f.ArtefactID)
		}
		mt := strings.TrimSpace(strings.SplitN(a.MediaType, ";", 2)[0])
		fmt.Fprintf(&b, "- %s (%s, %s, artefact %s): %s\n", a.Name, mt, humanSize(a.Size), a.ID, a.Path)
	}
	b.WriteString("</attached-files>")
	return b.String(), nil
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
