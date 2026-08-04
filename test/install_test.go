// This file covers the distribution/install story (ticket 9), as part of
// the same e2e package as e2e_test.go: it verifies the two documented
// install paths as far as is possible without a real GitHub remote —
//
//   - "go install" path: builds the binary the same way `go install`
//     would (via `go build ./cmd/canopy`) and separately exercises a real
//     `go install ./cmd/canopy` (module-relative form) into a scratch
//     GOPATH/bin, then runs the freshly installed binary through the
//     init -> claim -> status smoke test the ticket asks for.
//   - install.sh: its OS/arch detection and download-URL construction are
//     unit tested in test/install_script_test.sh (run here via `sh`),
//     since the actual curl download can't be exercised without a live
//     GitHub release.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot returns the absolute path to the repository root (this test's
// parent directory), the same way test/e2e_test.go locates it.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// newScratchRepo creates a minimal git repo in a fresh temp dir, mirroring
// test/e2e_test.go's newRepo helper (kept independent here so this file
// has no compile-time dependency on the e2e package).
func newScratchRepo(t *testing.T) string {
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

// smokeTest runs the ticket's required init -> claim -> status sequence
// against the given canopy binary in a fresh scratch repo, failing the
// test with full output on any non-zero exit.
func smokeTest(t *testing.T, binPath string) {
	t.Helper()
	repo := newScratchRepo(t)

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(binPath, args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v failed: %v\noutput:\n%s", binPath, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	run("init")

	claimOut := run("claim", "--holder", "smoke-test-holder")
	if claimOut == "" {
		t.Fatal("claim produced no output (expected a worktree path)")
	}
	if fi, err := os.Stat(claimOut); err != nil || !fi.IsDir() {
		t.Fatalf("claimed path %q is not a usable directory: %v", claimOut, err)
	}

	statusOut := run("status")
	if !strings.Contains(statusOut, "smoke-test-holder") {
		t.Fatalf("status output does not mention the claiming holder:\n%s", statusOut)
	}
}

// TestGoBuildSmokeTest is the direct verification of acceptance criterion
// 4 ("a freshly installed binary via either path passes a basic smoke
// test"), using the package-wide binPath (built once via `go build
// ./cmd/canopy` in TestMain) as the real, verifiable stand-in for both
// install paths: it's exactly what `go install` does under the hood, and
// it's what install.sh's release archives are built from via goreleaser.
func TestGoBuildSmokeTest(t *testing.T) {
	smokeTest(t, binPath)
}

// TestGoInstallSmokeTest verifies the actual `go install` code path (as
// opposed to `go build`, which TestGoBuildSmokeTest already covers): it
// runs `go install ./cmd/canopy` — the module-relative form, which is the
// closest local approximation to `go install github.com/asif/canopy/cmd/canopy@latest`
// available without a real GitHub remote to install from — into a scratch
// GOPATH/bin, then runs the resulting binary through the same smoke test.
//
// This is a real, verified `go install` run; what it does NOT verify is
// the network fetch-by-module-path-and-version step that `@latest` adds,
// since that requires a published, tagged remote module that doesn't
// exist yet for this repo.
func TestGoInstallSmokeTest(t *testing.T) {
	// Only redirect GOBIN to a scratch dir; leave GOPATH (and its module
	// cache) alone. Pointing GOPATH itself at a t.TempDir() makes `go
	// install` populate pkg/mod with read-only files that t.TempDir()'s
	// cleanup then fails to remove.
	gobin := t.TempDir()

	cmd := exec.Command("go", "install", "./cmd/canopy")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOBIN="+gobin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go install ./cmd/canopy: %v\n%s", err, out)
	}

	binPath := filepath.Join(gobin, "canopy")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("go install did not produce %s: %v", binPath, err)
	}

	smokeTest(t, binPath)
}

// TestInstallScriptUnitTests runs install.sh's own shell-based unit test
// suite (test/install_script_test.sh), which exercises OS/arch detection
// and download-URL construction against known os/arch/version combinations
// with `uname` stubbed out — see that file for what it covers and why a
// real end-to-end download isn't tested here (no live GitHub release to
// download from yet).
func TestInstallScriptUnitTests(t *testing.T) {
	scriptPath := filepath.Join(repoRoot(t), "test", "install_script_test.sh")
	cmd := exec.Command("sh", scriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install_script_test.sh failed: %v\n%s", err, out)
	}
	t.Log(string(out))
}
