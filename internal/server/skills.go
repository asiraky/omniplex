package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/asiraky/omniplex/internal/skills"
	"github.com/asiraky/omniplex/internal/thread"
	"github.com/asiraky/omniplex/internal/userconfig"
)

func (s *Server) skillCommand(ctx context.Context, command string, a skillArgs) (any, error) {
	// Saved first: every other command reads the roots the setup decides.
	if command == "save_skills_setup" {
		next := userconfig.SkillsConfig{Library: a.Library, ProjectLibrary: a.ProjectLibrary, CLIVersion: a.CLIVersion}
		if _, err := userconfig.Update(func(cur *userconfig.Config) error {
			cur.Skills = next
			return nil
		}); err != nil {
			return nil, err
		}
	}
	roots, err := s.mgr.SkillRoots(ctx, a.ThreadID, a.ProjectID)
	if err != nil {
		return nil, err
	}
	switch command {
	case "save_skills_setup":
		return map[string]any{"setup": skillsSetup(roots)}, nil
	case "link_library":
		if err := skills.LinkLibrary(roots, skills.Harness(a.Harness)); err != nil {
			return nil, err
		}
		return map[string]any{"setup": skillsSetup(roots)}, nil
	case "link_skill":
		return skills.LinkSkill(roots, a.Dir, skills.Harness(a.Harness))
	case "set_skill_invocation":
		return skills.SetInvocation(roots, a.Dir, a.Manual)
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
		subagents, err := skills.DiscoverSubagents(roots)
		if err != nil {
			return nil, err
		}
		return map[string]any{"skills": found, "subagents": subagents, "projectRoot": roots.ProjectRoot, "setup": skillsSetup(roots)}, nil
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
		return skills.Create(roots, a.Scope, a.Name, a.Description)

	case "stage_skills":
		return s.skillFetch.Stage(ctx, roots, a.Source)
	case "read_staged_file":
		content, binary, err := skills.ReadStagedFile(a.ID, a.Skill, a.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": content, "binary": binary}, nil
	case "install_staged":
		link := make([]skills.Harness, 0, len(a.Link))
		for _, h := range a.Link {
			link = append(link, skills.Harness(h))
		}
		placed, err := skills.InstallStaged(roots, a.ID, a.Skills, a.Scope, link, a.Replace)
		if err != nil {
			return nil, err
		}
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
		return map[string]any{"skills": updated}, nil
	}
	return nil, fmt.Errorf("unknown skill command %q", command)
}

// skillsSetup is the setup as the screen shows it. Outside a project the
// roots carry no project library, so the configured one is filled in here:
// the setting is still there to see and change.
func skillsSetup(roots skills.Roots) skills.Setup {
	setup := skills.DetectSetup(roots)
	if roots.ProjectRoot == "" {
		if cfg, err := userconfig.Load(); err == nil && cfg.Skills.ProjectLibrary != "" {
			setup.ProjectLibrary = cfg.Skills.ProjectLibrary
		}
	}
	return setup
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
