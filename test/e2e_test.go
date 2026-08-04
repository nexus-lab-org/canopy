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
