package userconfig

import "testing"

func TestExpandHomeResolvesTildeAndRefusesRelativePaths(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	for in, want := range map[string]string{"~": "/home/someone", "~/code/app": "/home/someone/code/app", "/srv/x/../y": "/srv/y"} {
		if got, err := ExpandHome(in); err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := ExpandHome("code/app"); err == nil {
		t.Errorf("relative path accepted as %q", got)
	}
}
