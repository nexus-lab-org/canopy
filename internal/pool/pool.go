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
