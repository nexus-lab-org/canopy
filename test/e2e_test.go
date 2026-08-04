// Package e2e exercises the built canopy binary's externally observable
// behavior: CLI stdout/stderr/exit code and the resulting state.json,
// per the project's testing decisions (test the CLI contract, not
// internal package APIs).
package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var binPath string

// TestMain builds the canopy binary once for the whole package, rather
// than once per test case.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "canopy-e2e-bin-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	binPath = filepath.Join(dir, "canopy")
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		panic(err)
	}
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/canopy")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("building canopy binary: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

// result captures the outcome of a canopy invocation.
type result struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
}

func runCanopy(t *testing.T, dir string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
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

// runCanopyLivePID runs canopy with an explicit --pid pointing at a
// process this test itself keeps alive for the duration of the call,
// so claims made this way are reliably "live" for release tests.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-q", "-m", "initial commit")
	return dir
}

type stateFile struct {
	Version   int `json:"version"`
	Worktrees []struct {
		Path      string `json:"path"`
		Branch    string `json:"branch"`
		CreatedAt string `json:"created_at"`
		Claim     *struct {
			Holder    string `json:"holder"`
			PID       int    `json:"pid"`
			ClaimedAt string `json:"claimed_at"`
		} `json:"claim"`
	} `json:"worktrees"`
}

func readState(t *testing.T, repoDir string) stateFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoDir, ".git", "canopy", "state.json"))
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	var s stateFile
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("state.json is not valid JSON: %v\ncontents: %s", err, data)
	}
	return s
}

func TestInit(t *testing.T) {
	t.Run("creates state.json in a fresh repo", func(t *testing.T) {
		dir := newRepo(t)
		res := runCanopy(t, dir, "init")
		if res.exitCode != 0 {
			t.Fatalf("init failed: exit=%d stderr=%q", res.exitCode, res.stderr)
		}
		path := filepath.Join(dir, ".git", "canopy", "state.json")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("state.json not created: %v", err)
		}
		s := readState(t, dir)
		if s.Version == 0 {
			t.Errorf("expected a version field, got 0")
		}
		if len(s.Worktrees) != 0 {
			t.Errorf("expected empty pool on fresh init, got %d worktrees", len(s.Worktrees))
		}
	})

	t.Run("running init again is a no-op / clear message, not corruption", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")
		before := readState(t, dir)

		res := runCanopy(t, dir, "init")
		// Either a clean no-op (exit 0) or a clear error (non-zero) is
		// acceptable per the ticket; what matters is state isn't corrupted
		// and the message is informative.
		if res.exitCode != 0 && !strings.Contains(strings.ToLower(res.stdout+res.stderr), "already") {
			t.Errorf("expected a clear 'already initialized' message, got stdout=%q stderr=%q", res.stdout, res.stderr)
		}

		after := readState(t, dir)
		if len(after.Worktrees) != len(before.Worktrees) {
			t.Errorf("second init mutated the pool: before=%d after=%d", len(before.Worktrees), len(after.Worktrees))
		}
	})
}

func TestClaim(t *testing.T) {
	t.Run("returns a usable worktree path on a new branch and records the claim", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		res := runCanopy(t, dir, "claim", "--holder", "session-a")
		if res.exitCode != 0 {
			t.Fatalf("claim failed: stderr=%q", res.stderr)
		}
		path := res.stdout
		if path == "" {
			t.Fatal("claim printed no path")
		}
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			t.Fatalf("claimed path %q is not a usable directory: %v", path, err)
		}
		// The worktree should be a real, checked-out git worktree.
		if _, err := os.Stat(filepath.Join(path, "README.md")); err != nil {
			t.Errorf("claimed worktree missing checked-out files: %v", err)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 1 {
			t.Fatalf("expected 1 worktree in state, got %d", len(s.Worktrees))
		}
		wt := s.Worktrees[0]
		if wt.Path != path {
			t.Errorf("state path %q != returned path %q", wt.Path, path)
		}
		if wt.Branch == "" {
			t.Errorf("expected worktree to be on a named branch")
		}
		if wt.Claim == nil {
			t.Fatal("expected a claim record")
		}
		if wt.Claim.Holder != "session-a" {
			t.Errorf("claim holder = %q, want session-a", wt.Claim.Holder)
		}
		if wt.Claim.PID == 0 {
			t.Errorf("expected a nonzero PID recorded")
		}
		if wt.Claim.ClaimedAt == "" {
			t.Errorf("expected claimed_at to be set")
		}
	})

	t.Run("a second claim with a different holder gets a different worktree", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		r1 := runCanopy(t, dir, "claim", "--holder", "session-a")
		r2 := runCanopy(t, dir, "claim", "--holder", "session-b")
		if r1.exitCode != 0 || r2.exitCode != 0 {
			t.Fatalf("claims failed: %q %q", r1.stderr, r2.stderr)
		}
		if r1.stdout == r2.stdout {
			t.Fatalf("both claims got the same worktree path: %q", r1.stdout)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 2 {
			t.Fatalf("expected 2 worktrees, got %d", len(s.Worktrees))
		}
		holders := map[string]bool{}
		for _, wt := range s.Worktrees {
			if wt.Claim == nil {
				t.Fatalf("expected both worktrees claimed, found a free one: %+v", wt)
			}
			holders[wt.Claim.Holder] = true
		}
		if !holders["session-a"] || !holders["session-b"] {
			t.Errorf("expected claims for both session-a and session-b, got %v", holders)
		}
	})

	t.Run("requires --holder", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")
		res := runCanopy(t, dir, "claim")
		if res.exitCode == 0 {
			t.Fatal("expected claim without --holder to fail")
		}
	})
}

func TestClaimMax(t *testing.T) {
	t.Run("grows the pool one worktree at a time up to --max, then errors clearly", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		const max = 3
		for i := 0; i < max; i++ {
			holder := "holder-" + strconv.Itoa(i)
			res := runCanopy(t, dir, "claim", "--holder", holder, "--max", strconv.Itoa(max))
			if res.exitCode != 0 {
				t.Fatalf("claim %d/%d failed: stderr=%q", i+1, max, res.stderr)
			}
		}
		s := readState(t, dir)
		if len(s.Worktrees) != max {
			t.Fatalf("expected pool grown to %d worktrees, got %d", max, len(s.Worktrees))
		}

		// One more claim, with the pool already at max and nothing free,
		// must error clearly rather than hang or exceed max.
		res := runCanopy(t, dir, "claim", "--holder", "one-too-many", "--max", strconv.Itoa(max))
		if res.exitCode == 0 {
			t.Fatalf("expected claim beyond --max to fail, got path %q", res.stdout)
		}
		msg := strings.ToLower(res.stderr)
		if !strings.Contains(msg, "exhaust") && !strings.Contains(msg, "max") {
			t.Errorf("expected a clear pool-exhaustion error, got stderr=%q", res.stderr)
		}

		after := readState(t, dir)
		if len(after.Worktrees) != max {
			t.Fatalf("expected pool to stay at %d worktrees after refused claim, got %d", max, len(after.Worktrees))
		}
	})

	t.Run("releasing a worktree after hitting --max allows a subsequent claim to succeed again", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		const max = 2
		var claims []result
		for i := 0; i < max; i++ {
			holder := "holder-" + strconv.Itoa(i)
			res := runCanopy(t, dir, "claim", "--holder", holder, "--max", strconv.Itoa(max))
			if res.exitCode != 0 {
				t.Fatalf("claim %d/%d failed: stderr=%q", i+1, max, res.stderr)
			}
			claims = append(claims, res)
		}

		// Pool is at max; further claim should fail.
		full := runCanopy(t, dir, "claim", "--holder", "blocked", "--max", strconv.Itoa(max))
		if full.exitCode == 0 {
			t.Fatalf("expected claim at max to fail, got path %q", full.stdout)
		}

		// Release one of the earlier claims.
		rel := runCanopy(t, dir, "release", "--holder", "holder-0")
		if rel.exitCode != 0 {
			t.Fatalf("release failed: stderr=%q", rel.stderr)
		}

		// A subsequent claim should now succeed, reusing the freed worktree.
		reclaimed := runCanopy(t, dir, "claim", "--holder", "holder-new", "--max", strconv.Itoa(max))
		if reclaimed.exitCode != 0 {
			t.Fatalf("expected claim after release to succeed, got stderr=%q", reclaimed.stderr)
		}
		if reclaimed.stdout != claims[0].stdout {
			t.Errorf("expected reclaim to reuse the freed worktree %q, got %q", claims[0].stdout, reclaimed.stdout)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != max {
			t.Fatalf("expected pool to stay at %d worktrees (reused, not grown), got %d", max, len(s.Worktrees))
		}
	})

	t.Run("--max 0 (default) means unlimited: claims keep succeeding", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		for i := 0; i < 4; i++ {
			holder := "holder-" + strconv.Itoa(i)
			res := runCanopy(t, dir, "claim", "--holder", holder)
			if res.exitCode != 0 {
				t.Fatalf("claim %d without --max failed: stderr=%q", i, res.stderr)
			}
		}
		s := readState(t, dir)
		if len(s.Worktrees) != 4 {
			t.Fatalf("expected pool grown to 4 worktrees with no --max set, got %d", len(s.Worktrees))
		}
	})
}

func TestRelease(t *testing.T) {
	t.Run("frees the holder's worktree back to the pool for a future claim", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}

		rel := runCanopy(t, dir, "release", "--holder", "session-a")
		if rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 1 {
			t.Fatalf("expected the worktree to remain in the pool, got %d worktrees", len(s.Worktrees))
		}
		if s.Worktrees[0].Claim != nil {
			t.Fatalf("expected worktree to be unclaimed after release, got claim %+v", s.Worktrees[0].Claim)
		}

		// A subsequent claim should reuse the freed worktree rather than
		// creating a new one.
		reclaimed := runCanopy(t, dir, "claim", "--holder", "session-c")
		if reclaimed.exitCode != 0 {
			t.Fatalf("re-claim failed: %s", reclaimed.stderr)
		}
		if reclaimed.stdout != claimed.stdout {
			t.Errorf("expected re-claim to reuse freed worktree %q, got %q", claimed.stdout, reclaimed.stdout)
		}
		s2 := readState(t, dir)
		if len(s2.Worktrees) != 1 {
			t.Errorf("expected pool to still have exactly 1 worktree (reused, not grown), got %d", len(s2.Worktrees))
		}
	})

	t.Run("refuses to release a worktree it doesn't hold a matching claim for", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")
		runCanopy(t, dir, "claim", "--holder", "session-a")

		res := runCanopy(t, dir, "release", "--holder", "no-such-holder")
		if res.exitCode == 0 {
			t.Fatal("expected release for a non-matching holder to fail")
		}

		s := readState(t, dir)
		if s.Worktrees[0].Claim == nil || s.Worktrees[0].Claim.Holder != "session-a" {
			t.Fatalf("original claim should be untouched, got %+v", s.Worktrees[0].Claim)
		}
	})

	t.Run("without --force refuses a claim whose recorded PID has died", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		// Claim with an explicit PID that we control and can kill, to
		// deterministically produce a stale (dead-PID) claim.
		deadPID := spawnAndKill(t)
		res := runCanopy(t, dir, "claim", "--holder", "session-a", "--pid", strconv.Itoa(deadPID))
		if res.exitCode != 0 {
			t.Fatalf("claim failed: %s", res.stderr)
		}

		rel := runCanopy(t, dir, "release", "--holder", "session-a")
		if rel.exitCode == 0 {
			t.Fatal("expected release without --force to refuse a claim with a dead PID")
		}

		s := readState(t, dir)
		if s.Worktrees[0].Claim == nil {
			t.Fatal("claim should still be present after refused release")
		}
	})

	t.Run("--force releases a claim regardless of the recorded PID's liveness", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		deadPID := spawnAndKill(t)
		runCanopy(t, dir, "claim", "--holder", "session-a", "--pid", strconv.Itoa(deadPID))

		rel := runCanopy(t, dir, "release", "--force", "--holder", "session-a")
		if rel.exitCode != 0 {
			t.Fatalf("release --force failed: %s", rel.stderr)
		}
		s := readState(t, dir)
		if s.Worktrees[0].Claim != nil {
			t.Fatalf("expected claim to be cleared after --force release, got %+v", s.Worktrees[0].Claim)
		}
	})

	t.Run("self-release with a live PID succeeds without --force", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		// Default PID (parent of the claim invocation, i.e. this test
		// process) stays alive for the duration of the test.
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		rel := runCanopy(t, dir, "release", "--holder", "session-a")
		if rel.exitCode != 0 {
			t.Fatalf("expected release with a live PID to succeed without --force, got: %s", rel.stderr)
		}
	})
}

// gitIn runs git with args inside dir, failing the test on error, and
// returns combined output (rarely needed, but useful for debugging a
// failing assertion).
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return string(out)
}

// commitFile writes content to name inside dir and commits it on
// whatever branch dir currently has checked out — used to give a
// worktree's branch a commit that hasn't landed on the repo's default
// branch, so destroy's unmerged-branch check has something to refuse.
func commitFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-q", "-m", "add "+name)
}

// worktreeExists reports whether git (not just the filesystem) still
// considers path a registered worktree of the repo at repoDir.
func worktreeExists(t *testing.T, repoDir, path string) bool {
	t.Helper()
	out := gitIn(t, repoDir, "worktree", "list", "--porcelain")
	return strings.Contains(out, path)
}

type destroyResult struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	Destroyed bool   `json:"destroyed"`
	Reason    string `json:"reason"`
}

type destroyReport struct {
	Destroyed []destroyResult `json:"destroyed"`
	Skipped   []destroyResult `json:"skipped"`
}

func TestDestroy(t *testing.T) {
	t.Run("removes a clean, merged, unclaimed worktree from disk and git", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout
		if rel := runCanopy(t, dir, "release", "--holder", "session-a"); rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}

		res := runCanopy(t, dir, "destroy", wtPath)
		if res.exitCode != 0 {
			t.Fatalf("destroy failed: %s", res.stderr)
		}

		if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
			t.Errorf("expected worktree directory to be removed, stat err = %v", err)
		}
		if worktreeExists(t, dir, wtPath) {
			t.Errorf("expected worktree to be unregistered from git, still present")
		}
		s := readState(t, dir)
		if len(s.Worktrees) != 0 {
			t.Errorf("expected worktree removed from state.json catalog, got %d entries", len(s.Worktrees))
		}
	})

	t.Run("fails on a nonexistent/unmanaged path", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		res := runCanopy(t, dir, "destroy", filepath.Join(dir, "not-a-worktree"))
		if res.exitCode == 0 {
			t.Fatal("expected destroy of an unmanaged path to fail")
		}
	})

	t.Run("refuses an unmerged branch unless --include-unlanded is passed", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout
		commitFile(t, wtPath, "feature.txt", "unmerged work\n")
		if rel := runCanopy(t, dir, "release", "--holder", "session-a"); rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}

		refused := runCanopy(t, dir, "destroy", wtPath)
		if refused.exitCode == 0 {
			t.Fatalf("expected destroy of an unmerged branch to fail without --include-unlanded")
		}
		if !strings.Contains(strings.ToLower(refused.stderr), "merged") {
			t.Errorf("expected a clear unmerged-branch error, got stderr=%q", refused.stderr)
		}
		if !worktreeExists(t, dir, wtPath) {
			t.Fatal("worktree should still be registered after a refused destroy")
		}

		allowed := runCanopy(t, dir, "destroy", wtPath, "--include-unlanded")
		if allowed.exitCode != 0 {
			t.Fatalf("expected destroy with --include-unlanded to succeed: %s", allowed.stderr)
		}
		if worktreeExists(t, dir, wtPath) {
			t.Error("expected worktree to be gone after destroy --include-unlanded")
		}
	})

	t.Run("refuses a dirty working tree unless --include-dirty is passed", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout
		if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("writing scratch file: %v", err)
		}
		if rel := runCanopy(t, dir, "release", "--holder", "session-a"); rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}

		refused := runCanopy(t, dir, "destroy", wtPath)
		if refused.exitCode == 0 {
			t.Fatalf("expected destroy of a dirty worktree to fail without --include-dirty")
		}
		if !strings.Contains(strings.ToLower(refused.stderr), "dirty") && !strings.Contains(strings.ToLower(refused.stderr), "uncommitted") {
			t.Errorf("expected a clear dirty-worktree error, got stderr=%q", refused.stderr)
		}
		if !worktreeExists(t, dir, wtPath) {
			t.Fatal("worktree should still be registered after a refused destroy")
		}

		allowed := runCanopy(t, dir, "destroy", wtPath, "--include-dirty")
		if allowed.exitCode != 0 {
			t.Fatalf("expected destroy with --include-dirty to succeed: %s", allowed.stderr)
		}
		if worktreeExists(t, dir, wtPath) {
			t.Error("expected worktree to be gone after destroy --include-dirty")
		}
	})

	t.Run("a worktree that is both unmerged and dirty requires both flags together", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout
		commitFile(t, wtPath, "feature.txt", "unmerged work\n")
		if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("writing scratch file: %v", err)
		}
		if rel := runCanopy(t, dir, "release", "--holder", "session-a"); rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}

		if res := runCanopy(t, dir, "destroy", wtPath); res.exitCode == 0 {
			t.Fatal("expected destroy with neither flag to fail")
		}
		if res := runCanopy(t, dir, "destroy", wtPath, "--include-unlanded"); res.exitCode == 0 {
			t.Fatal("expected destroy with only --include-unlanded to fail (still dirty)")
		}
		if res := runCanopy(t, dir, "destroy", wtPath, "--include-dirty"); res.exitCode == 0 {
			t.Fatal("expected destroy with only --include-dirty to fail (still unmerged)")
		}
		if !worktreeExists(t, dir, wtPath) {
			t.Fatal("worktree should still be registered after every refused destroy attempt")
		}

		res := runCanopy(t, dir, "destroy", wtPath, "--include-unlanded", "--include-dirty")
		if res.exitCode != 0 {
			t.Fatalf("expected destroy with both flags to succeed: %s", res.stderr)
		}
		if worktreeExists(t, dir, wtPath) {
			t.Error("expected worktree to be gone after destroy with both flags")
		}
	})

	t.Run("refuses a worktree with a live claim, without touching it", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		// Default PID (this test process's parent) stays alive for the
		// duration of the test, so this claim is live.
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout

		res := runCanopy(t, dir, "destroy", wtPath, "--include-unlanded", "--include-dirty")
		if res.exitCode == 0 {
			t.Fatal("expected destroy of a live-claimed worktree to fail")
		}
		msg := strings.ToLower(res.stderr)
		if !strings.Contains(msg, "live claim") && !strings.Contains(msg, "release") {
			t.Errorf("expected an error pointing at release --force, got stderr=%q", res.stderr)
		}

		if !worktreeExists(t, dir, wtPath) {
			t.Fatal("live-claimed worktree should still be registered")
		}
		s := readState(t, dir)
		if s.Worktrees[0].Claim == nil || s.Worktrees[0].Claim.Holder != "session-a" {
			t.Fatalf("live claim should be untouched, got %+v", s.Worktrees[0].Claim)
		}
	})

	t.Run("--all-idle applies the same rules across every unclaimed worktree, leaving claimed ones alone", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		// Claim all four up front (rather than claim-then-release one at a
		// time) so the pool auto-grows to four distinct worktrees instead
		// of a release freeing one up for the next claim to reuse.
		destroyable := runCanopy(t, dir, "claim", "--holder", "destroyable")
		if destroyable.exitCode != 0 {
			t.Fatalf("claim failed: %s", destroyable.stderr)
		}
		unmerged := runCanopy(t, dir, "claim", "--holder", "unmerged-holder")
		if unmerged.exitCode != 0 {
			t.Fatalf("claim failed: %s", unmerged.stderr)
		}
		dirty := runCanopy(t, dir, "claim", "--holder", "dirty-holder")
		if dirty.exitCode != 0 {
			t.Fatalf("claim failed: %s", dirty.stderr)
		}
		// Claimed: live, left alone regardless of clean/merged state.
		live := runCanopy(t, dir, "claim", "--holder", "live-holder")
		if live.exitCode != 0 {
			t.Fatalf("claim failed: %s", live.stderr)
		}

		commitFile(t, unmerged.stdout, "feature.txt", "unmerged\n")
		if err := os.WriteFile(filepath.Join(dirty.stdout, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("writing scratch file: %v", err)
		}

		// destroyable: clean, merged (no new commits), unclaimed.
		if rel := runCanopy(t, dir, "release", "--holder", "destroyable"); rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}
		// unmerged: clean (committed), unmerged, unclaimed.
		if rel := runCanopy(t, dir, "release", "--holder", "unmerged-holder"); rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}
		// dirty: merged, dirty (uncommitted), unclaimed.
		if rel := runCanopy(t, dir, "release", "--holder", "dirty-holder"); rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}
		// live-holder stays claimed with a live PID (this test process's
		// parent, the default when --pid isn't passed).

		res := runCanopy(t, dir, "destroy", "--all-idle", "--json")
		if res.exitCode != 0 {
			t.Fatalf("destroy --all-idle failed: %s", res.stderr)
		}
		var report destroyReport
		if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
			t.Fatalf("destroy --all-idle --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(report.Destroyed) != 1 || report.Destroyed[0].Path != destroyable.stdout {
			t.Fatalf("expected exactly the destroyable worktree destroyed, got %+v", report.Destroyed)
		}
		if len(report.Skipped) != 2 {
			t.Fatalf("expected 2 skipped (unmerged + dirty), got %+v", report.Skipped)
		}
		skippedPaths := map[string]bool{}
		for _, e := range report.Skipped {
			skippedPaths[e.Path] = true
			if e.Reason == "" {
				t.Errorf("expected a reason for skipped entry %+v", e)
			}
		}
		if !skippedPaths[unmerged.stdout] || !skippedPaths[dirty.stdout] {
			t.Errorf("expected unmerged and dirty worktrees both skipped, got %+v", report.Skipped)
		}
		// The live-claimed worktree must not appear in the report at all.
		for _, e := range append(report.Destroyed, report.Skipped...) {
			if e.Path == live.stdout {
				t.Errorf("expected the live-claimed worktree to be absent from the report entirely, found %+v", e)
			}
		}

		if worktreeExists(t, dir, destroyable.stdout) {
			t.Error("expected the destroyable worktree to actually be gone from git")
		}
		if !worktreeExists(t, dir, unmerged.stdout) || !worktreeExists(t, dir, dirty.stdout) || !worktreeExists(t, dir, live.stdout) {
			t.Error("expected skipped and claimed worktrees to remain registered")
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 3 {
			t.Fatalf("expected 3 worktrees left in the pool (unmerged, dirty, live), got %d", len(s.Worktrees))
		}

		// A follow-up run with both override flags should sweep up the
		// unmerged and dirty ones too, still leaving the live claim alone.
		res2 := runCanopy(t, dir, "destroy", "--all-idle", "--include-unlanded", "--include-dirty", "--json")
		if res2.exitCode != 0 {
			t.Fatalf("destroy --all-idle with both flags failed: %s", res2.stderr)
		}
		var report2 destroyReport
		if err := json.Unmarshal([]byte(res2.stdout), &report2); err != nil {
			t.Fatalf("destroy --all-idle --json output not valid JSON: %v\noutput: %s", err, res2.stdout)
		}
		if len(report2.Destroyed) != 2 || len(report2.Skipped) != 0 {
			t.Fatalf("expected both remaining idle worktrees destroyed with override flags, got %+v", report2)
		}

		s2 := readState(t, dir)
		if len(s2.Worktrees) != 1 || s2.Worktrees[0].Claim == nil || s2.Worktrees[0].Claim.Holder != "live-holder" {
			t.Fatalf("expected only the live-claimed worktree left in the pool, got %+v", s2.Worktrees)
		}
	})
}

// spawnAndKill starts a short-lived child process, waits for it to
// exit, and returns its now-dead PID.
func spawnAndKill(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawning helper process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("waiting for helper process: %v", err)
	}
	// Give the OS a moment; on Linux a reaped process's PID is
	// immediately reusable in theory but reuse this soon is vanishingly
	// unlikely in a test process with few forks.
	time.Sleep(10 * time.Millisecond)
	return pid
}

type statusEntry struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Holder   string `json:"holder"`
	Liveness string `json:"liveness"`
	Clean    bool   `json:"clean"`
}

func TestStatus(t *testing.T) {
	t.Run("lists claimed and idle worktrees", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		idlePath := runCanopy(t, dir, "claim", "--holder", "session-b")
		if idlePath.exitCode != 0 {
			t.Fatalf("claim failed: %s", idlePath.stderr)
		}
		rel := runCanopy(t, dir, "release", "--holder", "session-b")
		if rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}

		res := runCanopy(t, dir, "status")
		if res.exitCode != 0 {
			t.Fatalf("status failed: %s", res.stderr)
		}
		if !strings.Contains(res.stdout, "session-a") {
			t.Errorf("expected status output to list claimed holder session-a, got:\n%s", res.stdout)
		}
		if !strings.Contains(res.stdout, idlePath.stdout) {
			t.Errorf("expected status output to list idle worktree path %q, got:\n%s", idlePath.stdout, res.stdout)
		}
		// Two data lines plus a header.
		lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
		if len(lines) != 3 {
			t.Errorf("expected header + 2 worktree lines, got %d lines:\n%s", len(lines), res.stdout)
		}
	})

	t.Run("a claim held by a live PID reports live", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		// Default PID (this test process's parent) stays alive for the
		// duration of the test.
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}

		res := runCanopy(t, dir, "status", "--json")
		if res.exitCode != 0 {
			t.Fatalf("status --json failed: %s", res.stderr)
		}
		var entries []statusEntry
		if err := json.Unmarshal([]byte(res.stdout), &entries); err != nil {
			t.Fatalf("status --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 status entry, got %d", len(entries))
		}
		if entries[0].Liveness != "live" {
			t.Errorf("expected liveness=live for a claim held by a running PID, got %q", entries[0].Liveness)
		}
	})

	t.Run("a claim whose recorded PID has died reports stale", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		deadPID := spawnAndKill(t)
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a", "--pid", strconv.Itoa(deadPID))
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}

		res := runCanopy(t, dir, "status", "--json")
		if res.exitCode != 0 {
			t.Fatalf("status --json failed: %s", res.stderr)
		}
		var entries []statusEntry
		if err := json.Unmarshal([]byte(res.stdout), &entries); err != nil {
			t.Fatalf("status --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 status entry, got %d", len(entries))
		}
		if entries[0].Liveness != "stale" {
			t.Errorf("expected liveness=stale for a claim whose PID has died, got %q", entries[0].Liveness)
		}
	})

	t.Run("an idle (unclaimed) worktree is not reported as live or stale", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		rel := runCanopy(t, dir, "release", "--holder", "session-a")
		if rel.exitCode != 0 {
			t.Fatalf("release failed: %s", rel.stderr)
		}

		res := runCanopy(t, dir, "status", "--json")
		if res.exitCode != 0 {
			t.Fatalf("status --json failed: %s", res.stderr)
		}
		var entries []statusEntry
		if err := json.Unmarshal([]byte(res.stdout), &entries); err != nil {
			t.Fatalf("status --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 status entry, got %d", len(entries))
		}
		if entries[0].Holder != "" {
			t.Errorf("expected no holder for an unclaimed worktree, got %q", entries[0].Holder)
		}
		if entries[0].Liveness == "live" || entries[0].Liveness == "stale" {
			t.Errorf("expected an unclaimed worktree to not be reported live/stale, got %q", entries[0].Liveness)
		}
	})

	t.Run("clean vs dirty working tree detection", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout

		res := runCanopy(t, dir, "status", "--json")
		if res.exitCode != 0 {
			t.Fatalf("status --json failed: %s", res.stderr)
		}
		var entries []statusEntry
		if err := json.Unmarshal([]byte(res.stdout), &entries); err != nil {
			t.Fatalf("status --json output not valid JSON: %v", err)
		}
		if len(entries) != 1 || !entries[0].Clean {
			t.Fatalf("expected freshly claimed worktree to be clean, got %+v", entries)
		}

		// Make the working tree dirty.
		if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("writing scratch file: %v", err)
		}

		res2 := runCanopy(t, dir, "status", "--json")
		if res2.exitCode != 0 {
			t.Fatalf("status --json failed: %s", res2.stderr)
		}
		var entries2 []statusEntry
		if err := json.Unmarshal([]byte(res2.stdout), &entries2); err != nil {
			t.Fatalf("status --json output not valid JSON: %v", err)
		}
		if len(entries2) != 1 || entries2[0].Clean {
			t.Fatalf("expected worktree with an untracked file to be dirty, got %+v", entries2)
		}
	})

	t.Run("--json output parses as an array with all fields populated", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}

		res := runCanopy(t, dir, "status", "--json")
		if res.exitCode != 0 {
			t.Fatalf("status --json failed: %s", res.stderr)
		}
		var entries []statusEntry
		if err := json.Unmarshal([]byte(res.stdout), &entries); err != nil {
			t.Fatalf("status --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		e := entries[0]
		if e.Path == "" || e.Branch == "" || e.Holder != "session-a" || e.Liveness == "" {
			t.Errorf("expected all fields populated, got %+v", e)
		}
	})

	t.Run("empty pool status is an empty list, not an error", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		res := runCanopy(t, dir, "status", "--json")
		if res.exitCode != 0 {
			t.Fatalf("status --json failed: %s", res.stderr)
		}
		var entries []statusEntry
		if err := json.Unmarshal([]byte(res.stdout), &entries); err != nil {
			t.Fatalf("status --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(entries) != 0 {
			t.Errorf("expected empty pool to report 0 status entries, got %d", len(entries))
		}
	})
}

type pruneEntry struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Holder string `json:"holder"`
	Reason string `json:"reason"`
}

type pruneReport struct {
	Reclaimed []pruneEntry `json:"reclaimed"`
	Skipped   []pruneEntry `json:"skipped"`
}

func TestPrune(t *testing.T) {
	t.Run("a stale claim with a clean working tree is reclaimed and claimable afterward", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		deadPID := spawnAndKill(t)
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a", "--pid", strconv.Itoa(deadPID))
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}

		res := runCanopy(t, dir, "prune", "--json")
		if res.exitCode != 0 {
			t.Fatalf("prune failed: %s", res.stderr)
		}
		var report pruneReport
		if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
			t.Fatalf("prune --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(report.Reclaimed) != 1 || report.Reclaimed[0].Holder != "session-a" {
			t.Fatalf("expected session-a's worktree to be reclaimed, got %+v", report)
		}
		if len(report.Skipped) != 0 {
			t.Fatalf("expected nothing skipped, got %+v", report.Skipped)
		}

		s := readState(t, dir)
		if len(s.Worktrees) != 1 {
			t.Fatalf("expected the worktree to remain in the pool, got %d", len(s.Worktrees))
		}
		if s.Worktrees[0].Claim != nil {
			t.Fatalf("expected claim cleared after prune, got %+v", s.Worktrees[0].Claim)
		}

		// The reclaimed worktree should now be claimable by someone else.
		reclaimed := runCanopy(t, dir, "claim", "--holder", "session-b")
		if reclaimed.exitCode != 0 {
			t.Fatalf("claim after prune failed: %s", reclaimed.stderr)
		}
		if reclaimed.stdout != claimed.stdout {
			t.Errorf("expected re-claim to reuse the pruned worktree %q, got %q", claimed.stdout, reclaimed.stdout)
		}
		s2 := readState(t, dir)
		if len(s2.Worktrees) != 1 {
			t.Errorf("expected pool to stay at 1 worktree (reused, not grown), got %d", len(s2.Worktrees))
		}
	})

	t.Run("a stale claim with a dirty working tree is left claimed and reported as skipped", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		deadPID := spawnAndKill(t)
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a", "--pid", strconv.Itoa(deadPID))
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout
		if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("writing scratch file: %v", err)
		}

		res := runCanopy(t, dir, "prune", "--json")
		if res.exitCode != 0 {
			t.Fatalf("prune failed: %s", res.stderr)
		}
		var report pruneReport
		if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
			t.Fatalf("prune --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(report.Reclaimed) != 0 {
			t.Fatalf("expected nothing reclaimed, got %+v", report.Reclaimed)
		}
		if len(report.Skipped) != 1 || report.Skipped[0].Holder != "session-a" || report.Skipped[0].Reason == "" {
			t.Fatalf("expected session-a's worktree to be reported skipped with a reason, got %+v", report)
		}

		s := readState(t, dir)
		if s.Worktrees[0].Claim == nil || s.Worktrees[0].Claim.Holder != "session-a" {
			t.Fatalf("expected stale-but-dirty claim to remain untouched, got %+v", s.Worktrees[0].Claim)
		}
	})

	t.Run("a live claim is never touched by prune, regardless of clean/dirty state", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		// Default PID (this test process's parent) stays alive for the
		// duration of the test, so this claim is live.
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a")
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout
		if err := os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("writing scratch file: %v", err)
		}

		res := runCanopy(t, dir, "prune", "--json")
		if res.exitCode != 0 {
			t.Fatalf("prune failed: %s", res.stderr)
		}
		var report pruneReport
		if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
			t.Fatalf("prune --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(report.Reclaimed) != 0 || len(report.Skipped) != 0 {
			t.Fatalf("expected a live claim to be neither reclaimed nor skipped, got %+v", report)
		}

		s := readState(t, dir)
		if s.Worktrees[0].Claim == nil || s.Worktrees[0].Claim.Holder != "session-a" {
			t.Fatalf("expected live claim to remain untouched, got %+v", s.Worktrees[0].Claim)
		}
	})

	t.Run("never deletes a worktree directory or its git worktree registration", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		deadPID := spawnAndKill(t)
		claimed := runCanopy(t, dir, "claim", "--holder", "session-a", "--pid", strconv.Itoa(deadPID))
		if claimed.exitCode != 0 {
			t.Fatalf("claim failed: %s", claimed.stderr)
		}
		wtPath := claimed.stdout

		res := runCanopy(t, dir, "prune")
		if res.exitCode != 0 {
			t.Fatalf("prune failed: %s", res.stderr)
		}

		if fi, err := os.Stat(wtPath); err != nil || !fi.IsDir() {
			t.Fatalf("expected worktree directory to still exist after prune: %v", err)
		}

		out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").CombinedOutput()
		if err != nil {
			t.Fatalf("git worktree list failed: %v: %s", err, out)
		}
		if !strings.Contains(string(out), wtPath) {
			t.Errorf("expected pruned worktree to remain registered in git, got:\n%s", out)
		}
	})

	t.Run("--json output parses and reflects reclaimed vs skipped for a mixed pool", func(t *testing.T) {
		dir := newRepo(t)
		runCanopy(t, dir, "init")

		// Stale + clean: reclaimable.
		deadPID1 := spawnAndKill(t)
		reclaimable := runCanopy(t, dir, "claim", "--holder", "reclaim-me", "--pid", strconv.Itoa(deadPID1))
		if reclaimable.exitCode != 0 {
			t.Fatalf("claim failed: %s", reclaimable.stderr)
		}

		// Stale + dirty: skip.
		deadPID2 := spawnAndKill(t)
		skippable := runCanopy(t, dir, "claim", "--holder", "skip-me", "--pid", strconv.Itoa(deadPID2))
		if skippable.exitCode != 0 {
			t.Fatalf("claim failed: %s", skippable.stderr)
		}
		if err := os.WriteFile(filepath.Join(skippable.stdout, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("writing scratch file: %v", err)
		}

		// Live: untouched.
		live := runCanopy(t, dir, "claim", "--holder", "leave-me")
		if live.exitCode != 0 {
			t.Fatalf("claim failed: %s", live.stderr)
		}

		res := runCanopy(t, dir, "prune", "--json")
		if res.exitCode != 0 {
			t.Fatalf("prune failed: %s", res.stderr)
		}
		var report pruneReport
		if err := json.Unmarshal([]byte(res.stdout), &report); err != nil {
			t.Fatalf("prune --json output not valid JSON: %v\noutput: %s", err, res.stdout)
		}
		if len(report.Reclaimed) != 1 || report.Reclaimed[0].Holder != "reclaim-me" {
			t.Fatalf("expected exactly reclaim-me reclaimed, got %+v", report.Reclaimed)
		}
		if len(report.Skipped) != 1 || report.Skipped[0].Holder != "skip-me" {
			t.Fatalf("expected exactly skip-me skipped, got %+v", report.Skipped)
		}

		s := readState(t, dir)
		holderClaims := map[string]bool{}
		for _, wt := range s.Worktrees {
			if wt.Claim != nil {
				holderClaims[wt.Claim.Holder] = true
			}
		}
		if holderClaims["reclaim-me"] {
			t.Errorf("expected reclaim-me's claim cleared, still present in state")
		}
		if !holderClaims["skip-me"] {
			t.Errorf("expected skip-me's claim to remain, missing from state")
		}
		if !holderClaims["leave-me"] {
			t.Errorf("expected leave-me's (live) claim to remain, missing from state")
		}
	})
}

func TestConcurrentClaims(t *testing.T) {
	dir := newRepo(t)
	runCanopy(t, dir, "init")

	const n = 12
	var wg sync.WaitGroup
	paths := make([]string, n)
	errs := make([]result, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := runCanopy(t, dir, "claim", "--holder", "holder-"+strconv.Itoa(i))
			errs[i] = res
			paths[i] = res.stdout
		}(i)
	}
	wg.Wait()

	for i, res := range errs {
		if res.exitCode != 0 {
			t.Fatalf("concurrent claim %d failed: %s", i, res.stderr)
		}
	}

	seen := map[string]int{}
	for _, p := range paths {
		if p == "" {
			t.Fatal("a concurrent claim returned an empty path")
		}
		seen[p]++
	}
	for p, count := range seen {
		if count > 1 {
			t.Errorf("worktree %q was handed out %d times", p, count)
		}
	}

	s := readState(t, dir)
	if len(s.Worktrees) != n {
		t.Fatalf("expected %d worktrees after %d concurrent claims, got %d", n, n, len(s.Worktrees))
	}
	holders := map[string]int{}
	for _, wt := range s.Worktrees {
		if wt.Claim == nil {
			t.Errorf("worktree %q has no claim after concurrent claims", wt.Path)
			continue
		}
		holders[wt.Claim.Holder]++
	}
	if len(holders) != n {
		t.Fatalf("expected %d distinct holders recorded, got %d: %v", n, len(holders), holders)
	}
	for h, count := range holders {
		if count != 1 {
			t.Errorf("holder %q recorded %d times, want 1", h, count)
		}
	}
}

func TestConcurrentClaimRelease(t *testing.T) {
	// Repeated claim/release cycles against the same pool, run
	// concurrently, should never corrupt state.json (must stay valid
	// JSON throughout) and every worktree must end up consistently
	// accounted for.
	dir := newRepo(t)
	runCanopy(t, dir, "init")

	const holders = 8
	var wg sync.WaitGroup
	for i := 0; i < holders; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			holder := "cycler-" + strconv.Itoa(i)
			claimed := runCanopy(t, dir, "claim", "--holder", holder)
			if claimed.exitCode != 0 {
				t.Errorf("claim for %s failed: %s", holder, claimed.stderr)
				return
			}
			released := runCanopy(t, dir, "release", "--holder", holder)
			if released.exitCode != 0 {
				t.Errorf("release for %s failed: %s", holder, released.stderr)
			}
		}(i)
	}
	wg.Wait()

	// state.json must still parse cleanly and every worktree should now
	// be free (all cyclers released).
	s := readState(t, dir)
	if len(s.Worktrees) != holders {
		t.Fatalf("expected %d worktrees in pool, got %d", holders, len(s.Worktrees))
	}
	for _, wt := range s.Worktrees {
		if wt.Claim != nil {
			t.Errorf("expected all worktrees free after claim+release cycles, %q still claimed by %q", wt.Path, wt.Claim.Holder)
		}
	}
}
