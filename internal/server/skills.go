package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/asiraky/omniplex/internal/projection"
	"github.com/asiraky/omniplex/internal/session"
	"github.com/asiraky/omniplex/internal/skills"
)

func (s *Server) skillCommand(ctx context.Context, command string, a skillArgs) (any, error) {
	roots, err := s.mgr.SkillRoots(ctx, a.SessionID, a.ProjectID)
	if err != nil {
		return nil, err
	}
	switch command {
	case "list_skills":
		found, err := skills.Discover(roots)
		if err != nil {
			return nil, err
		}
		subagents, err := skills.DiscoverSubagents(roots)
		if err != nil {
			return nil, err
		}
		return map[string]any{"skills": found, "subagents": subagents, "projectRoot": roots.ProjectRoot}, nil
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
	}
	return nil, fmt.Errorf("unknown skill command %q", command)
}

// attachedFiles is the trailer a prompt carries for the files a human
// attached: what each is and where it is on this host, so the agent can read
// it. The web client parses the same block back out to draw the files as
// cards on the message.
func (s *Server) attachedFiles(ctx context.Context, actor *session.Actor, sessionID string, files []promptFile) (string, error) {
	if s.artefacts == nil {
		return "", fmt.Errorf("this server does not store artefacts")
	}
	st, err := actor.State(ctx)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("\n\n<attached-files>\n")
	for _, f := range files {
		a, ok := st.ArtefactByID(f.ArtefactID)
		if !ok {
			return "", fmt.Errorf("no such file %s", f.ArtefactID)
		}
		var v *projection.ArtefactVersion
		for i := range a.Versions {
			if a.Versions[i].Version == f.Version {
				v = &a.Versions[i]
			}
		}
		if v == nil {
			return "", fmt.Errorf("no version %d of %s", f.Version, a.Name)
		}
		p, err := s.artefacts.Open(sessionID, a.ID, f.Version, v.Entry)
		if err != nil {
			return "", err
		}
		mt := strings.TrimSpace(strings.SplitN(v.MediaType, ";", 2)[0])
		fmt.Fprintf(&b, "- %s (%s, %s, artefact %s@%d): %s\n", a.Name, mt, humanSize(v.Size), a.ID, f.Version, p)
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
