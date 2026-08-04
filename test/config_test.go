package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runCanopyEnv is like runCanopy but lets the caller override/extend the
// process environment (e.g. HOME, to isolate user-level config.toml
// lookups from the real developer's ~/.config/canopy/).
func runCanopyEnv(t *testing.T, dir string, extraEnv []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("running canopy %v: %v", args, err)
		}
	}
	return result{
		stdout:   strings.TrimSpace(stdout.String()),
		stderr:   strings.TrimSpace(stderr.String()),
		exitCode: code,
		err:      err,
	}
}

// isolatedHome creates a fresh, empty $HOME (with no
// ~/.config/canopy/config.toml) for a test to write its own into,
// keeping tests from ever reading the real developer's user-level
// config.
func isolatedHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// writeUserConfig writes home/.config/canopy/config.toml with the given
// contents.
func writeUserConfig(t *testing.T, home, contents string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "canopy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeRepoConfig writes repoDir/canopy.toml with the given contents.
func writeRepoConfig(t *testing.T, repoDir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repoDir, "canopy.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigMaxFromRepoFile(t *testing.T) {
	t.Run("claim's effective --max comes from repo-level canopy.toml when no --max flag is passed", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		writeRepoConfig(t, dir, "max = 2\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		for i := 0; i < 2; i++ {
			res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "h"+strconv.Itoa(i))
			if res.exitCode != 0 {
				t.Fatalf("claim %d failed: stderr=%q", i, res.stderr)
			}
		}

		// Pool should now be at the configured max of 2; a third claim
		// with no --more free worktrees and no --max flag must be
		// refused using the repo-level max, not fall back to unlimited.
		res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "one-too-many")
		if res.exitCode == 0 {
			t.Fatalf("expected claim beyond canopy.toml's max=2 to fail (no --max flag passed), got path %q", res.stdout)
		}
		msg := strings.ToLower(res.stdout + res.stderr)
		if !strings.Contains(msg, "exhaust") && !strings.Contains(msg, "max") {
			t.Errorf("expected a clear max-related error, got stdout=%q stderr=%q", res.stdout, res.stderr)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 2 {
			t.Fatalf("expected pool to stay at 2 worktrees, got %d", len(s.Worktrees))
		}
	})

	t.Run("an explicit --max flag still overrides canopy.toml", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		writeRepoConfig(t, dir, "max = 1\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		// canopy.toml says max=1, but an explicit --max 2 flag should win.
		for i := 0; i < 2; i++ {
			res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "h"+strconv.Itoa(i), "--max", "2")
			if res.exitCode != 0 {
				t.Fatalf("claim %d with explicit --max 2 failed: stderr=%q", i, res.stderr)
			}
		}
		s := readState(t, dir)
		if len(s.Worktrees) != 2 {
			t.Fatalf("expected explicit --max 2 to override canopy.toml's max=1, got %d worktrees", len(s.Worktrees))
		}
	})
}

func TestConfigBranchNaming(t *testing.T) {
	t.Run("auto-created branches follow the naming scheme configured in canopy.toml", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		writeRepoConfig(t, dir, `branch_naming = "agents/{holder}-session"`+"\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "alice")
		if res.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", res.stderr)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 1 {
			t.Fatalf("expected 1 worktree, got %d", len(s.Worktrees))
		}
		want := "agents/alice-session"
		if s.Worktrees[0].Branch != want {
			t.Errorf("expected branch %q per canopy.toml's branch_naming, got %q", want, s.Worktrees[0].Branch)
		}
	})

	t.Run("default branch naming is unchanged when no canopy.toml is present", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "bob")
		if res.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", res.stderr)
		}

		s := readState(t, dir)
		want := "canopy/bob"
		if s.Worktrees[0].Branch != want {
			t.Errorf("expected default branch %q, got %q", want, s.Worktrees[0].Branch)
		}
	})
}

func TestConfigWorktreeBaseDir(t *testing.T) {
	t.Run("new worktrees are created under the base directory configured in user-level config.toml", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		base := filepath.Join(home, "custom-worktrees")
		writeUserConfig(t, home, `worktree_base_dir = "`+base+`"`+"\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "carol")
		if res.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", res.stderr)
		}

		if !strings.HasPrefix(res.stdout, base) {
			t.Errorf("expected worktree path under configured base dir %q, got %q", base, res.stdout)
		}
		if _, err := os.Stat(res.stdout); err != nil {
			t.Errorf("worktree not created at reported path: %v", err)
		}
	})

	t.Run("default worktree location is unchanged when no config.toml is present", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "dave")
		if res.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", res.stderr)
		}

		wantDir := filepath.Dir(dir)
		if !strings.HasPrefix(res.stdout, wantDir) {
			t.Errorf("expected default sibling worktree dir under %q, got %q", wantDir, res.stdout)
		}
	})
}

func TestConfigHooks(t *testing.T) {
	t.Run("a post_create hook defined in user-level config.toml runs after a successful claim", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		marker := filepath.Join(home, "post-create-marker.txt")
		writeUserConfig(t, home, `[hooks]`+"\n"+
			`post_create = "echo \"$CANOPY_HOLDER $CANOPY_WORKTREE_PATH\" > `+marker+`"`+"\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "erin")
		if res.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", res.stderr)
		}

		data, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("expected post_create hook to have run and written %s: %v", marker, err)
		}
		got := strings.TrimSpace(string(data))
		want := "erin " + res.stdout
		if got != want {
			t.Errorf("post_create hook env: got %q, want %q", got, want)
		}
	})

	t.Run("a pre_destroy hook defined in user-level config.toml runs before destroy removes a worktree", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		marker := filepath.Join(home, "pre-destroy-marker.txt")
		writeUserConfig(t, home, `[hooks]`+"\n"+
			`pre_destroy = "echo \"$CANOPY_WORKTREE_PATH\" > `+marker+`"`+"\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		claimRes := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "frank")
		if claimRes.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", claimRes.stderr)
		}
		wtPath := claimRes.stdout

		releaseRes := runCanopyEnv(t, dir, []string{"HOME=" + home}, "release", "--holder", "frank")
		if releaseRes.exitCode != 0 {
			t.Fatalf("release failed: stderr=%q", releaseRes.stderr)
		}

		destroyRes := runCanopyEnv(t, dir, []string{"HOME=" + home}, "destroy", wtPath)
		if destroyRes.exitCode != 0 {
			t.Fatalf("destroy failed: stderr=%q", destroyRes.stderr)
		}

		data, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("expected pre_destroy hook to have run and written %s: %v", marker, err)
		}
		got := strings.TrimSpace(string(data))
		if got != wtPath {
			t.Errorf("pre_destroy hook env: got CANOPY_WORKTREE_PATH=%q, want %q", got, wtPath)
		}

		// The worktree itself should really be gone (the hook runs
		// before removal, not instead of it).
		if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
			t.Errorf("expected worktree %s to be removed by destroy, stat err=%v", wtPath, err)
		}
	})

	t.Run("a post_create/pre_destroy hook defined in repo-level canopy.toml is ignored, never executed", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		marker := filepath.Join(home, "should-not-exist.txt")
		writeRepoConfig(t, dir, `[hooks]`+"\n"+
			`post_create = "touch `+marker+`"`+"\n"+
			`pre_destroy = "touch `+marker+`"`+"\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		res := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "grace")
		if res.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", res.stderr)
		}

		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("repo-level post_create hook was executed (marker file exists), but repo-level hooks must never run")
		}
		if !strings.Contains(strings.ToLower(res.stderr), "hook") {
			t.Errorf("expected a warning about the ignored repo-level hook on stderr, got %q", res.stderr)
		}

		// destroy should likewise never run the repo-level pre_destroy hook.
		releaseRes := runCanopyEnv(t, dir, []string{"HOME=" + home}, "release", "--holder", "grace")
		if releaseRes.exitCode != 0 {
			t.Fatalf("release failed: stderr=%q", releaseRes.stderr)
		}
		destroyRes := runCanopyEnv(t, dir, []string{"HOME=" + home}, "destroy", res.stdout, "--include-unlanded")
		if destroyRes.exitCode != 0 {
			t.Fatalf("destroy failed: stderr=%q", destroyRes.stderr)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("repo-level pre_destroy hook was executed (marker file exists), but repo-level hooks must never run")
		}
	})
}

func TestConfigPrecedence(t *testing.T) {
	t.Run("repo-level values take precedence over user-level values for the same key (max)", func(t *testing.T) {
		dir := newRepo(t)
		home := isolatedHome(t)
		writeUserConfig(t, home, "max = 5\n")
		writeRepoConfig(t, dir, "max = 1\n")
		runCanopyEnv(t, dir, []string{"HOME=" + home}, "init")

		first := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "one")
		if first.exitCode != 0 {
			t.Fatalf("first claim failed: stderr=%q", first.stderr)
		}

		second := runCanopyEnv(t, dir, []string{"HOME=" + home}, "claim", "--holder", "two")
		if second.exitCode == 0 {
			t.Fatalf("expected second claim to be refused (repo-level max=1 should win over user-level max=5), got path %q", second.stdout)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 1 {
			t.Fatalf("expected pool capped at repo-level max=1, got %d worktrees", len(s.Worktrees))
		}
	})
}
