// Package arch enforces the dependency direction the design depends on.
// These are build failures, not conventions.
package arch

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const module = "github.com/asiraky/omniplex"

// core packages must never learn that a particular harness exists. They may
// depend on the adapter contract; they may not depend on any implementation
// of it.
var corePackages = []string{
	"internal/thread",
	"internal/server",
	"internal/store",
	"internal/projection",
	"internal/proto",
	"internal/jsonrpc",
}

// adapterImplPrefix is the namespace every concrete harness lives under.
const adapterImplPrefix = module + "/internal/adapter/"

func TestCoreDoesNotImportAnyHarness(t *testing.T) {
	root := repoRoot(t)

	for _, pkg := range corePackages {
		dir := filepath.Join(root, pkg)
		imports := importsOf(t, dir)

		for _, imp := range imports {
			if strings.HasPrefix(imp, adapterImplPrefix) {
				t.Errorf("%s imports %s\n\n"+
					"Core packages must depend only on the adapter contract "+
					"(%s/internal/adapter), never on a concrete harness. "+
					"Whatever this needs belongs behind the Adapter interface.",
					pkg, imp, module)
			}
		}
	}
}

// TestAdaptersDoNotImportCore is the other half of invariant 10: an adapter
// that knows the log, the fanout, or a connection exists is a bug.
func TestAdaptersDoNotImportCore(t *testing.T) {
	root := repoRoot(t)
	forbidden := []string{
		module + "/internal/thread",
		module + "/internal/server",
		module + "/internal/store",
		module + "/internal/projection",
	}

	adapterDir := filepath.Join(root, "internal", "adapter")
	entries, err := os.ReadDir(adapterDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkg := filepath.Join("internal", "adapter", e.Name())
		for _, imp := range importsOf(t, filepath.Join(root, pkg)) {
			for _, bad := range forbidden {
				if imp == bad {
					t.Errorf("%s imports %s\n\n"+
						"Adapters emit canonical events and answer host callbacks. "+
						"They never touch the log, the fanout, or a connection.",
						pkg, imp)
				}
			}
		}
	}
}

// TestOnlyCompositionRootNamesHarnesses keeps harness names out of everything
// but the one place whose job is wiring.
func TestOnlyCompositionRootNamesHarnesses(t *testing.T) {
	root := repoRoot(t)

	for _, pkg := range corePackages {
		for _, imp := range importsOf(t, filepath.Join(root, pkg)) {
			if strings.Contains(imp, "claudecode") || strings.Contains(imp, "codexapp") {
				t.Errorf("%s imports %s; only cmd/omniplex may name a harness", pkg, imp)
			}
		}
	}
}

func importsOf(t *testing.T, dir string) []string {
	t.Helper()
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		if _, ok := err.(*build.NoGoError); ok {
			return nil
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	return append(append([]string{}, pkg.Imports...), pkg.TestImports...)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod")
		}
		dir = parent
	}
}
