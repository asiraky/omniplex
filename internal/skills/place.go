package skills

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// copySkill copies a skill folder to dst. It is a copy, not a rename, so what
// lands in a library is plain files that owe nothing to where they came from:
// .git is left behind, and a symlink is kept only when it points at something
// else inside the same skill. One that leads out is refused, since it would
// either dangle once staging is gone or reach into the rest of the machine.
func copySkill(src, dst string) error {
	top, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	return filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(top, p)
		if err != nil {
			return err
		}
		if p != top && d.Name() == ".git" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		to := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			return os.MkdirAll(to, 0o755)
		case mode&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			escapes := fmt.Errorf("%w: %s is a link to outside the skill", ErrInvalid, filepath.ToSlash(rel))
			// Both as written and as resolved: a link that leaves and comes
			// back would point somewhere else once it is in the library.
			if filepath.IsAbs(target) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), target)) {
				return escapes
			}
			real, err := filepath.EvalSymlinks(p)
			if err != nil || (real != top && !within(top, real)) {
				return escapes
			}
			return os.Symlink(target, to)
		case mode.IsRegular():
			return copyFile(p, to, mode.Perm())
		}
		return fmt.Errorf("%w: %s is not a file", ErrInvalid, filepath.ToSlash(rel))
	})
}

func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copyBeside copies a skill folder to a hidden sibling of where it is going,
// so a copy that fails half way has touched nothing a harness reads.
func copyBeside(src, library, name string) (string, error) {
	tmp, err := os.MkdirTemp(library, "."+name+".*.tmp")
	if err != nil {
		return "", err
	}
	if err := copySkill(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	return tmp, nil
}

// swapIn moves a finished copy into place, over whatever is there. The old
// skill goes back if the new one cannot take its place.
func swapIn(tmp, dst string) error {
	old := ""
	if _, err := os.Lstat(dst); err == nil {
		old = tmp + ".old"
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if old != "" {
			_ = os.Rename(old, dst)
		}
		return err
	}
	if old != "" {
		return os.RemoveAll(old)
	}
	return nil
}

func stamp() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// InstallStaged copies the picked skills out of a staging dir into the
// personal library, over any skill of the same name already there, records
// where they came from, makes them visible to every harness, and drops the
// staging dir.
func InstallStaged(r Roots, id string, names []string) ([]Skill, error) {
	st, err := openStage(id)
	if err != nil {
		return nil, err
	}
	if st.m.Update != nil {
		return nil, fmt.Errorf("%w: that fetch was for an update", ErrInvalid)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: pick at least one skill", ErrInvalid)
	}
	if r.Library == "" {
		return nil, fmt.Errorf("%w: no home directory", ErrInvalid)
	}
	library := r.Library
	if err := os.MkdirAll(library, 0o755); err != nil {
		return nil, err
	}
	if library, err = filepath.EvalSymlinks(library); err != nil {
		return nil, err
	}

	// Everything that can refuse does so before the library is touched.
	type placing struct {
		sf     stagedFolder
		folder string
		target string
		hash   string
	}
	var plan []placing
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		sf, ok := st.skill(name)
		if !ok {
			return nil, fmt.Errorf("%w: %s is not in the fetched copy", ErrInvalid, name)
		}
		// The name becomes a folder in the library, and it came out of
		// somebody else's repo.
		if err := ValidateName(sf.Name); err != nil {
			return nil, fmt.Errorf("%w: %q cannot be a skill's folder: %v", ErrInvalid, sf.Name, err)
		}
		folder, err := st.folder(sf)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(library, sf.Name)
		// A link is replaced like a folder: swapIn moves the link itself,
		// never what it points at.
		if info, err := os.Lstat(target); err == nil && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("%w: %s in %s is not a folder this can replace", ErrInvalid, sf.Name, abbreviate(library, r.Home))
		}
		hash, err := HashDir(folder)
		if err != nil {
			return nil, err
		}
		plan = append(plan, placing{sf: sf, folder: folder, target: target, hash: hash})
	}

	tmps := make([]string, 0, len(plan))
	defer func() {
		for _, tmp := range tmps {
			_ = os.RemoveAll(tmp) // only the ones that never made it into place
		}
	}()
	for _, p := range plan {
		tmp, err := copyBeside(p.folder, library, p.sf.Name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.sf.Name, err)
		}
		tmps = append(tmps, tmp)
	}
	for i, p := range plan {
		if err := swapIn(tmps[i], p.target); err != nil {
			return nil, fmt.Errorf("%s: %w", p.sf.Name, err)
		}
	}

	now := stamp()
	err = UpdateRecord(library, func(rec *Record) error {
		for _, p := range plan {
			ref := p.sf.Ref
			if ref == "" {
				ref = st.m.Ref
			}
			rec.Set(p.sf.Name, RecordEntry{Method: st.m.Method, Repo: st.m.Repo, Ref: ref, Path: p.sf.Path, Hash: p.hash, InstalledAt: now, UpdatedAt: now})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]Skill, 0, len(plan))
	for _, p := range plan {
		reachEveryAgent(r, p.target)
	}
	for _, p := range plan {
		s, err := find(r, p.target)
		if err != nil {
			return nil, fmt.Errorf("installed %s but it is not discoverable: %w", p.sf.Name, err)
		}
		out = append(out, s)
	}
	_ = os.RemoveAll(st.dir)
	return out, nil
}
