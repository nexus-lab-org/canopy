package pool

import (
	"path/filepath"
	"testing"
)

func TestRepoSubdir(t *testing.T) {
	t.Run("includes the repo basename for readability", func(t *testing.T) {
		got := repoSubdir("/home/asif/code/maxasif-co/canopy")
		if filepath.Base(got) != got {
			t.Fatalf("repoSubdir must return a single path segment, got %q", got)
		}
		want := "canopy-"
		if len(got) <= len(want) || got[:len(want)] != want {
			t.Errorf("expected subdir to start with %q, got %q", want, got)
		}
	})

	t.Run("two repos with the same basename get different subdirs", func(t *testing.T) {
		a := repoSubdir("/home/asif/code/work/canopy")
		b := repoSubdir("/home/asif/code/side/canopy")
		if a == b {
			t.Errorf("expected different subdirs for repos with the same basename at different paths, both got %q", a)
		}
	})

	t.Run("is deterministic for the same path", func(t *testing.T) {
		a := repoSubdir("/home/asif/code/maxasif-co/canopy")
		b := repoSubdir("/home/asif/code/maxasif-co/canopy")
		if a != b {
			t.Errorf("expected repoSubdir to be deterministic, got %q then %q", a, b)
		}
	})
}

func TestDefaultWorktreeRoot(t *testing.T) {
	t.Run("respects XDG_DATA_HOME when set", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/xdg-data")
		got, err := defaultWorktreeRoot()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join("/xdg-data", "canopy", "worktrees")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("falls back to ~/.local/share when XDG_DATA_HOME is unset", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/testuser")
		got, err := defaultWorktreeRoot()
		if err != nil {
			t.Fatal(err)
		}
		want := filepath.Join("/home/testuser", ".local", "share", "canopy", "worktrees")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
