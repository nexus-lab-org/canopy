// Package hooks exercises hooks/claim.sh and hooks/release.sh — the
// shell scripts that wire canopy into Claude Code's and Codex's
// SessionStart/SubagentStart/Stop/SubagentStop hooks (see hooks/README.md)
// — against a real temp canopy repo, the same way test/e2e_test.go
// exercises the canopy binary directly: by shelling out to the real
// artifact (here, the hook scripts) and inspecting resulting stdout/
// state.json, not internal function calls.
package hooks

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var (
	binPath       string // built canopy binary
	claimScript   string // hooks/claim.sh
	releaseScript string // hooks/release.sh
	binDir        string // directory containing the canopy binary, for PATH
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "canopy-hooks-bin-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		panic(err)
	}

	binPath = filepath.Join(dir, "canopy")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/canopy")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("building canopy binary: " + err.Error() + "\n" + string(out))
	}
	binDir = dir

	claimScript = filepath.Join(repoRoot, "hooks", "claim.sh")
	releaseScript = filepath.Join(repoRoot, "hooks", "release.sh")
	if _, err := os.Stat(claimScript); err != nil {
		panic("hooks/claim.sh not found: " + err.Error())
	}
	if _, err := os.Stat(releaseScript); err != nil {
		panic("hooks/release.sh not found: " + err.Error())
	}

	os.Exit(m.Run())
}

// newRepo sets up a fresh git repo with canopy initialized, mirroring
// test/e2e_test.go's helper of the same purpose.
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

	initCmd := exec.Command(binPath, "init")
	initCmd.Dir = dir
	if out, err := initCmd.CombinedOutput(); err != nil {
		t.Fatalf("canopy init: %v: %s", err, out)
	}
	return dir
}

type claimRecord struct {
	Holder    string `json:"holder"`
	PID       int    `json:"pid"`
	ClaimedAt string `json:"claimed_at"`
}

type stateFile struct {
	Worktrees []struct {
		Path  string       `json:"path"`
		Claim *claimRecord `json:"claim"`
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

// claimFor returns the claim record (if any) held by holder across every
// worktree in the pool.
func claimFor(s stateFile, holder string) *claimRecord {
	for _, wt := range s.Worktrees {
		if wt.Claim != nil && wt.Claim.Holder == holder {
			return wt.Claim
		}
	}
	return nil
}

// runHook runs the given hook script (claimScript or releaseScript) with
// payload piped to its stdin, in a subprocess whose cwd is repoDir and
// whose PATH includes the built canopy binary plus the real system PATH
// (for jq, sh, etc.) — the same runtime shape a real Claude Code/Codex
// hook invocation has: canopy resolvable on PATH, invoked with a JSON
// payload on stdin.
func runHook(t *testing.T, script, repoDir, payload string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(script)
	cmd.Dir = repoDir
	cmd.Stdin = strings.NewReader(payload)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("running hook script %s: %v", script, err)
		}
	}
	return outBuf.String(), errBuf.String(), code
}

func sessionStartPayload(sessionID, cwd string) string {
	return `{"session_id":"` + sessionID + `","transcript_path":"/tmp/x.jsonl","cwd":"` + cwd + `","hook_event_name":"SessionStart","source":"startup","model":"claude-sonnet-5"}`
}

func stopPayload(sessionID, cwd string) string {
	return `{"session_id":"` + sessionID + `","transcript_path":"/tmp/x.jsonl","cwd":"` + cwd + `","permission_mode":"default","hook_event_name":"Stop","stop_hook_active":false,"last_assistant_message":"done"}`
}

func subagentStartPayload(sessionID, agentID, cwd string) string {
	return `{"session_id":"` + sessionID + `","transcript_path":"/tmp/x.jsonl","cwd":"` + cwd + `","hook_event_name":"SubagentStart","agent_id":"` + agentID + `","agent_type":"Explore"}`
}

func subagentStopPayload(sessionID, agentID, cwd string) string {
	return `{"session_id":"` + sessionID + `","transcript_path":"/tmp/x.jsonl","cwd":"` + cwd + `","permission_mode":"default","hook_event_name":"SubagentStop","stop_hook_active":false,"agent_id":"` + agentID + `","agent_type":"Explore","agent_transcript_path":"/tmp/sub.jsonl","last_assistant_message":"done"}`
}

func TestClaimHook(t *testing.T) {
	t.Run("a fabricated SessionStart payload claims a worktree for that session id", func(t *testing.T) {
		dir := newRepo(t)
		payload := sessionStartPayload("sess-abc123", dir)

		stdout, stderr, code := runHook(t, claimScript, dir, payload)
		if code != 0 {
			t.Fatalf("claim.sh exited %d: stdout=%q stderr=%q", code, stdout, stderr)
		}

		s := readState(t, dir)
		claim := claimFor(s, "sess-abc123")
		if claim == nil {
			t.Fatalf("expected a claim recorded for holder sess-abc123, got worktrees %+v", s.Worktrees)
		}
		if claim.PID == 0 {
			t.Errorf("expected a nonzero PID recorded on the claim")
		}
	})

	t.Run("a fabricated SubagentStart payload uses agent_id as the holder, not session_id", func(t *testing.T) {
		dir := newRepo(t)
		payload := subagentStartPayload("sess-parent", "agent-child-1", dir)

		_, stderr, code := runHook(t, claimScript, dir, payload)
		if code != 0 {
			t.Fatalf("claim.sh exited %d: stderr=%q", code, stderr)
		}

		s := readState(t, dir)
		if claimFor(s, "agent-child-1") == nil {
			t.Fatalf("expected a claim recorded for holder agent-child-1, got %+v", s.Worktrees)
		}
		if claimFor(s, "sess-parent") != nil {
			t.Errorf("did not expect a claim recorded for the parent session id sess-parent")
		}
	})
}

func TestReleaseHook(t *testing.T) {
	t.Run("a fabricated Stop payload releases the matching session's claim", func(t *testing.T) {
		dir := newRepo(t)
		claimPayload := sessionStartPayload("sess-xyz", dir)
		if _, stderr, code := runHook(t, claimScript, dir, claimPayload); code != 0 {
			t.Fatalf("setup claim failed: %s", stderr)
		}
		if claimFor(readState(t, dir), "sess-xyz") == nil {
			t.Fatal("setup claim did not take effect")
		}

		stopPayloadStr := stopPayload("sess-xyz", dir)
		_, stderr, code := runHook(t, releaseScript, dir, stopPayloadStr)
		if code != 0 {
			t.Fatalf("release.sh exited %d: stderr=%q", code, stderr)
		}

		s := readState(t, dir)
		if claimFor(s, "sess-xyz") != nil {
			t.Errorf("expected sess-xyz's claim to be released, still present: %+v", s.Worktrees)
		}
	})

	t.Run("a subagent claims and releases independently of a concurrently-claimed parent session", func(t *testing.T) {
		dir := newRepo(t)

		// Parent session claims and stays claimed throughout.
		parentPayload := sessionStartPayload("sess-parent-2", dir)
		if _, stderr, code := runHook(t, claimScript, dir, parentPayload); code != 0 {
			t.Fatalf("parent claim failed: %s", stderr)
		}

		// A subagent dispatched mid-session claims its own worktree.
		subStart := subagentStartPayload("sess-parent-2", "agent-sub-9", dir)
		if _, stderr, code := runHook(t, claimScript, dir, subStart); code != 0 {
			t.Fatalf("subagent claim failed: %s", stderr)
		}

		mid := readState(t, dir)
		if claimFor(mid, "sess-parent-2") == nil {
			t.Fatal("expected parent session's claim to still be present")
		}
		if claimFor(mid, "agent-sub-9") == nil {
			t.Fatal("expected subagent's own claim to be present")
		}
		parentPath := claimForPath(mid, "sess-parent-2")
		subPath := claimForPath(mid, "agent-sub-9")
		if parentPath == "" || subPath == "" || parentPath == subPath {
			t.Fatalf("expected parent and subagent to hold distinct worktrees, got parent=%q sub=%q", parentPath, subPath)
		}

		// Subagent finishes and releases; parent's claim must be untouched.
		subStop := subagentStopPayload("sess-parent-2", "agent-sub-9", dir)
		if _, stderr, code := runHook(t, releaseScript, dir, subStop); code != 0 {
			t.Fatalf("subagent release failed: %s", stderr)
		}

		after := readState(t, dir)
		if claimFor(after, "agent-sub-9") != nil {
			t.Errorf("expected subagent's claim released, still present: %+v", after.Worktrees)
		}
		if claimFor(after, "sess-parent-2") == nil {
			t.Fatalf("expected parent session's claim to remain untouched by the subagent's release")
		}
	})

	t.Run("a Stop payload for a holder with no matching claim warns but does not fail the hook", func(t *testing.T) {
		dir := newRepo(t)
		payload := stopPayload("never-claimed", dir)

		_, stderr, code := runHook(t, releaseScript, dir, payload)
		if code != 0 {
			t.Fatalf("expected release.sh to exit 0 even when release fails, got %d (stderr=%q)", code, stderr)
		}
		if stderr == "" {
			t.Errorf("expected a warning on stderr for a no-op release")
		}
	})
}

// claimForPath returns the worktree path held by holder, or "" if none.
func claimForPath(s stateFile, holder string) string {
	for _, wt := range s.Worktrees {
		if wt.Claim != nil && wt.Claim.Holder == holder {
			return wt.Path
		}
	}
	return ""
}
