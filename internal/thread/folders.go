package thread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/asiraky/omniplex/internal/project"
	"github.com/asiraky/omniplex/internal/proto"
	"github.com/asiraky/omniplex/internal/store"
	"github.com/asiraky/omniplex/internal/userconfig"
)

// NewProjectOptions says where a new project's first folder comes from. At
// most one of Path and URL is set; with neither, the project is a fresh home
// folder named after Name.
type NewProjectOptions struct {
	Name string `json:"name,omitempty"`
	// Path is a folder already on disk. A plain one becomes the home; a git
	// one is pointed at, and the home waits until it is needed.
	Path string `json:"path,omitempty"`
	// URL is a repository to clone into <home>/<repo>.
	URL string `json:"url,omitempty"`
}

// NewProject creates a project and its first folder.
func (m *Manager) NewProject(ctx context.Context, o NewProjectOptions) (project.Project, error) {
	name := strings.TrimSpace(o.Name)
	now := proto.NowMillis()
	p := project.Project{ID: uuid.NewString(), Name: name, Defaults: project.NormalizeDefaults(project.Defaults{}), CreatedAt: now, UpdatedAt: now}
	var folder string
	switch {
	case o.Path != "" && o.URL != "":
		return project.Project{}, errors.New("choose a folder or a repository, not both")
	case o.Path != "":
		abs, err := existingFolder(o.Path)
		if err != nil {
			return project.Project{}, err
		}
		if p.Name == "" {
			p.Name = filepath.Base(abs)
		}
		if !project.IsGit(abs) {
			p.Home = abs
		}
		folder = abs
	case o.URL != "":
		url, err := project.NormalizeRemote(o.URL)
		if err != nil {
			return project.Project{}, err
		}
		repo := project.DirectoryName(url)
		if repo == "" {
			return project.Project{}, errors.New("that URL does not name a repository")
		}
		if p.Name == "" {
			p.Name = repo
		}
		if p.Home, err = m.claimHome(ctx, p.Name); err != nil {
			return project.Project{}, err
		}
		folder = filepath.Join(p.Home, repo)
		if err := project.Clone(ctx, url, folder, m.logf); err != nil {
			// The home was made for this clone and holds nothing else.
			_ = os.Remove(p.Home)
			return project.Project{}, err
		}
	default:
		if p.Name == "" {
			return project.Project{}, errors.New("give the project a name")
		}
		home, err := m.claimHome(ctx, p.Name)
		if err != nil {
			return project.Project{}, err
		}
		p.Home, folder = home, home
	}
	p.Folders = []project.Folder{project.NewFolder(uuid.NewString(), folder)}
	if err := m.store.CreateProject(ctx, p); err != nil {
		return project.Project{}, err
	}
	m.notifyProjects()
	return m.store.Project(ctx, p.ID)
}

// claimHome makes a new home folder in the projects folder and returns it.
// A taken name gets a number rather than an error.
func (m *Manager) claimHome(ctx context.Context, name string) (string, error) {
	homeMu.Lock()
	defer homeMu.Unlock()
	home, err := newHome(ctx, m.store, name)
	if err != nil {
		return "", err
	}
	return home, os.MkdirAll(home, 0o755)
}

// AddFolderOptions says where a folder added to a project comes from.
// Exactly one field is set.
type AddFolderOptions struct {
	// Path is a folder already on disk, pointed at where it is.
	Path string `json:"path,omitempty"`
	// URL is a repository to clone into the project's home folder.
	URL string `json:"url,omitempty"`
	// Name is a new, empty folder in the project's home folder.
	Name string `json:"name,omitempty"`
}

// AddFolder adds a folder to a project.
func (m *Manager) AddFolder(ctx context.Context, projectID string, o AddFolderOptions) (project.Project, error) {
	set := 0
	for _, v := range []string{o.Path, o.URL, o.Name} {
		if strings.TrimSpace(v) != "" {
			set++
		}
	}
	if set != 1 {
		return project.Project{}, errors.New("choose one of a folder, a repository or a new folder name")
	}
	p, err := m.store.Project(ctx, projectID)
	if err != nil {
		return project.Project{}, err
	}
	var path string
	switch {
	case o.Path != "":
		if path, err = existingFolder(o.Path); err != nil {
			return project.Project{}, err
		}
		if err := checkNesting(p, path); err != nil {
			return project.Project{}, err
		}
	case o.URL != "":
		url, err := project.NormalizeRemote(o.URL)
		if err != nil {
			return project.Project{}, err
		}
		repo := project.DirectoryName(url)
		if repo == "" {
			return project.Project{}, errors.New("that URL does not name a repository")
		}
		if path, err = m.freeInHome(ctx, p, repo); err != nil {
			return project.Project{}, err
		}
		if err := project.Clone(ctx, url, path, m.logf); err != nil {
			return project.Project{}, err
		}
	default:
		name := strings.TrimSpace(o.Name)
		if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			return project.Project{}, fmt.Errorf("%q cannot be a folder name", name)
		}
		if path, err = m.freeInHome(ctx, p, name); err != nil {
			return project.Project{}, err
		}
		if err := os.Mkdir(path, 0o755); err != nil {
			return project.Project{}, err
		}
	}
	if err := m.store.AddFolder(ctx, projectID, project.NewFolder(uuid.NewString(), path)); err != nil {
		return project.Project{}, err
	}
	m.notifyProjects()
	return m.store.Project(ctx, projectID)
}

// freeInHome is a path in the project's home folder nothing occupies yet,
// numbered when name is taken.
func (m *Manager) freeInHome(ctx context.Context, p project.Project, name string) (string, error) {
	home, err := ProjectHome(ctx, m.store, p.ID)
	if err != nil {
		return "", err
	}
	for i := 1; ; i++ {
		candidate := name
		if i > 1 {
			candidate = name + "-" + strconv.Itoa(i)
		}
		path := filepath.Join(home, candidate)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
	}
}

// checkNesting refuses a folder that would sit inside one of the project's
// git folders, or hold one: that repo's git would see the other folder as
// its own untracked files.
func checkNesting(p project.Project, path string) error {
	git := project.IsGit(path)
	for _, f := range p.Folders {
		switch {
		case f.Path == path:
			return fmt.Errorf("%s is already in %s", path, p.Name)
		case f.Git && within(path, f.Path):
			return fmt.Errorf("%s is inside %s, a git folder of this project", path, f.Path)
		case git && within(f.Path, path):
			return fmt.Errorf("%s holds %s, which is already in this project", path, f.Path)
		}
	}
	return nil
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// existingFolder resolves what someone typed or picked to an absolute folder
// that exists.
func existingFolder(path string) (string, error) {
	abs, err := userconfig.ExpandHome(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s does not exist", abs)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a folder", abs)
	}
	return abs, nil
}

// RemoveFolder takes a folder out of a project. Nothing on disk is touched.
func (m *Manager) RemoveFolder(ctx context.Context, projectID, folderID string) (project.Project, error) {
	p, err := m.store.Project(ctx, projectID)
	if err != nil {
		return project.Project{}, err
	}
	if _, ok := p.Folder(folderID); !ok {
		return project.Project{}, fmt.Errorf("folder %s: %w", folderID, store.ErrNotFound)
	}
	if len(p.Folders) == 1 {
		return project.Project{}, errors.New("a project needs a folder. Remove the project instead")
	}
	if err := m.store.RemoveFolder(ctx, projectID, folderID); err != nil {
		return project.Project{}, err
	}
	m.notifyProjects()
	return m.store.Project(ctx, projectID)
}

// GitHubRepo is one row of `gh repo list`, trimmed to what the picker shows.
type GitHubRepo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Private     bool   `json:"private,omitempty"`
}

// GitHubRepos lists the signed-in GitHub user's repositories, most recently
// pushed first, through the gh CLI on the machine running Omniplex.
func (m *Manager) GitHubRepos(ctx context.Context) ([]GitHubRepo, error) {
	cmd := exec.CommandContext(ctx, "gh", "repo", "list", "--limit", "100", "--json", "nameWithOwner,description,isPrivate")
	out, err := cmd.Output()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, errors.New("the GitHub CLI (gh) is not installed on this machine")
		}
		m.logf("gh repo list: %v", err)
		return nil, errors.New("gh repo list failed. Is gh signed in on this machine? Run gh auth login")
	}
	var rows []struct {
		NameWithOwner string `json:"nameWithOwner"`
		Description   string `json:"description"`
		IsPrivate     bool   `json:"isPrivate"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	repos := make([]GitHubRepo, len(rows))
	for i, r := range rows {
		repos[i] = GitHubRepo{Name: r.NameWithOwner, Description: r.Description, Private: r.IsPrivate}
	}
	return repos, nil
}
