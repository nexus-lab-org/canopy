// Package pool implements canopy's worktree pool: handing out worktrees
// on claim, creating new ones on demand, and returning them on release.
// All state mutations go through state.WithLock so concurrent claim/
// release calls against the same repo never race.
package pool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/asif/canopy/internal/gitutil"
	"github.com/asif/canopy/internal/state"
)

// ErrNoMatchingClaim is returned by Release when the given holder does
// not currently hold a claim on any worktree in the pool.
var ErrNoMatchingClaim = errors.New("canopy: no claim found for that holder")

// ErrClaimStale is returned by Release when the claim's recorded PID no
// longer looks alive and --force was not passed. A holder releasing its
// own claim always has a live PID (it's the process making the call, or
// a still-running parent of it); a dead PID means someone other than
// the original holder is asking to release a claim whose process ended
// without going through the normal release path (crashed, or its hook
// was never wired up) — that requires a human to confirm with --force,
// per the spec's "recover a worktree by hand" escape hatch.
var ErrClaimStale = errors.New("canopy: claim's holder process is no longer running; pass --force to confirm releasing it anyway")

// Pool operates on a single repo's canopy state.
type Pool struct {
	RepoRoot     string // working tree root of the primary repo
	GitCommonDir string // shared .git dir (same for repo and its worktrees)
	StatePath    string // path to state.json
	BaseDir      string // directory new worktrees are created under
}

// Open resolves a Pool for the repo containing dir (any directory inside
// the primary repo or one of its worktrees).
func Open(dir string) (*Pool, error) {
	toplevel, err := gitutil.Toplevel(dir)
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	commonDir, err := gitutil.CommonDir(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving git common dir: %w", err)
	}

	// Default worktree base directory: a sibling of the repo root, named
	// "<repo>-canopy-worktrees". The config ticket makes this
	// configurable; this is just a sensible default for now.
	base := filepath.Join(filepath.Dir(toplevel), filepath.Base(toplevel)+"-canopy-worktrees")

	return &Pool{
		RepoRoot:     toplevel,
		GitCommonDir: commonDir,
		StatePath:    state.Path(commonDir),
		BaseDir:      base,
	}, nil
}

// Init sets up .git/canopy/state.json for this repo.
func (p *Pool) Init() error {
	_, err := state.Init(p.GitCommonDir)
	return err
}

var branchSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitize(s string) string {
	s = branchSanitizer.ReplaceAllString(s, "-")
	if s == "" {
		s = "holder"
	}
	return s
}

// Claim hands out a worktree to holder, creating a new one on a fresh
// branch if none in the pool is currently free, and records the claim.
// max caps the total pool size (claimed + idle worktrees); once the pool
// has max worktrees and none are free, Claim refuses rather than
// growing further. max <= 0 means unlimited (no cap).
func (p *Pool) Claim(holder string, pid int, max int) (*state.Worktree, error) {
	if holder == "" {
		return nil, errors.New("canopy: --holder is required")
	}

	var claimed *state.Worktree
	err := state.WithLock(p.StatePath, func(s *state.State) error {
		// Prefer an existing free worktree.
		for _, wt := range s.Worktrees {
			if wt.Claim == nil {
				wt.Claim = &state.Claim{
					Holder:    holder,
					PID:       pid,
					ClaimedAt: time.Now().UTC(),
				}
				claimed = wt
				return nil
			}
		}

		// None free: the pool needs to grow. Refuse if that would exceed
		// the configured max.
		if max > 0 && len(s.Worktrees) >= max {
			return fmt.Errorf("canopy: pool exhausted: %d/%d worktrees claimed, max reached", len(s.Worktrees), max)
		}

		// Create a new worktree on a fresh branch.
		branch := uniqueBranch(s, holder)
		name := sanitize(branch)
		path := filepath.Join(p.BaseDir, name)

		if err := os.MkdirAll(p.BaseDir, 0o755); err != nil {
			return fmt.Errorf("creating worktree base dir: %w", err)
		}
		if err := gitutil.WorktreeAdd(p.RepoRoot, path, branch); err != nil {
			return fmt.Errorf("creating worktree: %w", err)
		}

		wt := &state.Worktree{
			Path:      path,
			Branch:    branch,
			CreatedAt: time.Now().UTC(),
			Claim: &state.Claim{
				Holder:    holder,
				PID:       pid,
				ClaimedAt: time.Now().UTC(),
			},
		}
		s.Worktrees = append(s.Worktrees, wt)
		claimed = wt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// WorktreeStatus is one worktree's reported status: its identity, who
// (if anyone) holds it, whether that claim's PID is still alive, and
// whether the working tree has uncommitted changes.
type WorktreeStatus struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	// Holder is "" when the worktree is unclaimed.
	Holder string `json:"holder,omitempty"`
	// Liveness is "live" or "stale" for a claimed worktree, reflecting
	// whether the claim's recorded PID is still running (per
	// internal/pool/liveness.go, the same check release --force gates
	// on). It is "idle" for an unclaimed worktree, where liveness is not
	// applicable.
	Liveness string `json:"liveness"`
	// Clean is true when `git status --porcelain` reports no
	// uncommitted changes in the worktree.
	Clean bool `json:"clean"`
}

// Status reports every worktree in the pool, claimed or not, with its
// claim holder (if any), PID liveness ("live"/"stale"/"idle"), and
// working-tree cleanliness.
func (p *Pool) Status() ([]*WorktreeStatus, error) {
	s, err := state.Load(p.StatePath)
	if err != nil {
		return nil, err
	}

	statuses := make([]*WorktreeStatus, 0, len(s.Worktrees))
	for _, wt := range s.Worktrees {
		st := &WorktreeStatus{
			Path:     wt.Path,
			Branch:   wt.Branch,
			Liveness: "idle",
		}
		if wt.Claim != nil {
			st.Holder = wt.Claim.Holder
			if isAlive(wt.Claim.PID) {
				st.Liveness = "live"
			} else {
				st.Liveness = "stale"
			}
		}
		clean, err := gitutil.IsClean(wt.Path)
		if err != nil {
			return nil, fmt.Errorf("checking git status for %s: %w", wt.Path, err)
		}
		st.Clean = clean
		statuses = append(statuses, st)
	}
	return statuses, nil
}

// PruneEntry describes one claimed worktree that prune considered:
// either reclaimed (stale claim, clean working tree) or skipped (stale
// claim, dirty working tree — Reason explains why it was left alone).
type PruneEntry struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Holder string `json:"holder"`
	Reason string `json:"reason,omitempty"`
}

// PruneReport summarizes the outcome of a Prune call: which stale+clean
// worktrees were returned to the pool, and which stale+dirty ones were
// left claimed and why. Live claims are never touched and never appear
// in either list.
type PruneReport struct {
	Reclaimed []*PruneEntry `json:"reclaimed"`
	Skipped   []*PruneEntry `json:"skipped"`
}

// Prune scans the pool for claims whose recorded PID is no longer alive
// (per the same isAlive check Status and Release use) and, for each,
// checks the working tree's cleanliness (via gitutil.IsClean, the same
// check Status uses). A stale claim on a clean working tree is reclaimed
// — its claim record is cleared, returning the worktree to the pool for
// a future Claim, exactly as Release does. A stale claim on a dirty
// working tree is left untouched and reported as skipped, since
// uncommitted changes on a dead claim are the signal that distinguishes
// "finished normally" from "crashed mid-edit" (per the spec's
// safe-to-reclaim definition). Live claims are never inspected for
// cleanliness or touched, regardless of what state their working tree
// is in.
//
// includeUnlanded is accepted for CLI-surface parity with destroy, but
// is currently a no-op: prune's reclaim decision is already
// clean-vs-dirty only (merged-upstream status is irrelevant, per the
// spec's safe-to-reclaim definition), and prune never touches disk or
// branches, so there is no unlanded-branch behavior for this flag to
// gate here. It's reserved for a possible future stricter default.
//
// The whole scan-and-reclaim runs inside a single state.WithLock
// critical section, so it stays consistent with claim/release's
// concurrency-safety invariant: no other canopy invocation can observe
// or race a partially-applied prune.
func (p *Pool) Prune(includeUnlanded bool) (*PruneReport, error) {
	report := &PruneReport{
		Reclaimed: []*PruneEntry{},
		Skipped:   []*PruneEntry{},
	}

	err := state.WithLock(p.StatePath, func(s *state.State) error {
		for _, wt := range s.Worktrees {
			if wt.Claim == nil {
				continue // already free; nothing for prune to do
			}
			if isAlive(wt.Claim.PID) {
				continue // live claim: never touched, never reported
			}

			clean, err := gitutil.IsClean(wt.Path)
			if err != nil {
				return fmt.Errorf("checking git status for %s: %w", wt.Path, err)
			}

			if clean {
				holder := wt.Claim.Holder
				wt.Claim = nil
				report.Reclaimed = append(report.Reclaimed, &PruneEntry{
					Path:   wt.Path,
					Branch: wt.Branch,
					Holder: holder,
				})
			} else {
				report.Skipped = append(report.Skipped, &PruneEntry{
					Path:   wt.Path,
					Branch: wt.Branch,
					Holder: wt.Claim.Holder,
					Reason: "stale claim but working tree is dirty",
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// uniqueBranch picks "canopy/<holder>", falling back to
// "canopy/<holder>-2", "-3", ... if that name is already taken by an
// existing worktree in the pool.
func uniqueBranch(s *state.State, holder string) string {
	base := "canopy/" + sanitize(holder)
	taken := make(map[string]bool, len(s.Worktrees))
	for _, wt := range s.Worktrees {
		taken[wt.Branch] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate] {
			return candidate
		}
	}
}

// Release returns holder's claimed worktree to the pool. Without force,
// it refuses when holder has no matching claim, or when the claim's
// recorded PID is no longer alive (a normal self-release always has a
// live PID; force is required to confirm releasing a claim whose
// process has already died). With force, it releases the claim
// unconditionally once a matching holder is found.
func (p *Pool) Release(holder string, force bool) (*state.Worktree, error) {
	if holder == "" {
		return nil, errors.New("canopy: --holder is required")
	}

	var released *state.Worktree
	err := state.WithLock(p.StatePath, func(s *state.State) error {
		var match *state.Worktree
		for _, wt := range s.Worktrees {
			if wt.Claim != nil && wt.Claim.Holder == holder {
				match = wt
				break
			}
		}
		if match == nil {
			return ErrNoMatchingClaim
		}
		if !force && !isAlive(match.Claim.PID) {
			return ErrClaimStale
		}
		match.Claim = nil
		released = match
		return nil
	})
	if err != nil {
		return nil, err
	}
	return released, nil
}
