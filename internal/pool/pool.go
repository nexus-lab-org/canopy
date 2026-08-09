// Package pool implements canopy's worktree pool: handing out worktrees
// on claim, creating new ones on demand, and returning them on release.
// All state mutations go through state.WithLock so concurrent claim/
// release calls against the same repo never race.
package pool

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nexus-lab-org/canopy/internal/config"
	"github.com/nexus-lab-org/canopy/internal/gitutil"
	"github.com/nexus-lab-org/canopy/internal/state"
)

// defaultBranchNaming is the branch-name template used when neither
// repo-level canopy.toml nor any other configuration specifies
// branch_naming. "{holder}" is replaced with the sanitized holder name.
const defaultBranchNaming = "canopy/{holder}"

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

	// BranchNaming is the template used for auto-created branch names
	// (e.g. "canopy/{holder}"), resolved from repo-level canopy.toml or
	// defaultBranchNaming if unconfigured.
	BranchNaming string
	// ConfigMax is the pool max configured in repo-level canopy.toml (0
	// if unconfigured). Command entry points use this as the fallback
	// when --max is not passed on the command line.
	ConfigMax int
	// Hooks are the post_create/pre_destroy commands configured in
	// user-level config.toml (both "" if unconfigured or no user config
	// exists). A hook defined in repo-level canopy.toml is never
	// surfaced here — see internal/config's package doc.
	Hooks config.Hooks
}

// Open resolves a Pool for the repo containing dir (any directory inside
// the primary repo or one of its worktrees), loading its repo-level and
// user-level configuration (see internal/config) to resolve the pool max,
// branch-naming scheme, worktree base directory, and hooks.
func Open(dir string) (*Pool, error) {
	toplevel, err := gitutil.Toplevel(dir)
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}
	commonDir, err := gitutil.CommonDir(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving git common dir: %w", err)
	}

	cfg, err := config.Load(toplevel)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	// Default worktree base directory: a sibling of the repo root, named
	// "<repo>-canopy-worktrees". config.toml's worktree_base_dir
	// overrides this.
	base := filepath.Join(filepath.Dir(toplevel), filepath.Base(toplevel)+"-canopy-worktrees")
	if cfg.WorktreeBaseDir != "" {
		base = cfg.WorktreeBaseDir
	}

	branchNaming := defaultBranchNaming
	if cfg.BranchNaming != "" {
		branchNaming = cfg.BranchNaming
	}

	return &Pool{
		RepoRoot:     toplevel,
		GitCommonDir: commonDir,
		StatePath:    state.Path(commonDir),
		BaseDir:      base,
		BranchNaming: branchNaming,
		ConfigMax:    cfg.Max,
		Hooks:        cfg.Hooks,
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
		branch := uniqueBranch(s, holder, p.BranchNaming)
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

	if p.Hooks.PostCreate != "" {
		runHook(p.Hooks.PostCreate, claimed.Path, holder)
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

// uniqueBranch renders template (e.g. "canopy/{holder}") with "{holder}"
// replaced by the sanitized holder name, falling back to "<rendered>-2",
// "-3", ... if that name is already taken by an existing worktree in the
// pool. If template contains no "{holder}" placeholder, the sanitized
// holder name is appended instead, so auto-created branches never
// collide across holders regardless of a misconfigured template.
func uniqueBranch(s *state.State, holder, template string) string {
	if template == "" {
		template = defaultBranchNaming
	}
	name := sanitize(holder)
	var base string
	if strings.Contains(template, "{holder}") {
		base = strings.ReplaceAll(template, "{holder}", name)
	} else {
		base = template + name
	}

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

// runHook runs command via `sh -c`, with CANOPY_WORKTREE_PATH and
// CANOPY_HOLDER (holder may be "" for a destroy of an idle, unclaimed
// worktree) set in its environment. Hook failures are non-fatal: canopy
// reports them as a warning on stderr but does not fail the surrounding
// claim/destroy operation, since a broken hook (e.g. a typo in
// config.toml) shouldn't leave a worktree permanently stuck mid-claim or
// block cleanup — the operation it's attached to has already succeeded
// by the time the hook runs.
func runHook(command, path, holder string) {
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(),
		"CANOPY_WORKTREE_PATH="+path,
		"CANOPY_HOLDER="+holder,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "canopy: warning: hook %q failed: %v: %s\n", command, err, strings.TrimSpace(stderr.String()))
	}
}

// DestroyOptions controls which of destroy's two independent safety
// checks are overridden. Both are opt-in and independent of each other
// by design (see the spec's rationale for splitting them rather than a
// single blanket --force): accepting "this branch was never merged" and
// accepting "this has uncommitted changes" are two distinct, deliberate
// choices.
type DestroyOptions struct {
	IncludeUnlanded bool // destroy even if the branch isn't merged into the default branch
	IncludeDirty    bool // destroy even if the working tree has uncommitted changes
}

// DestroyResult describes the outcome for one worktree: either
// destroyed, or left alone with Reason explaining why.
type DestroyResult struct {
	Path      string `json:"path"`
	Branch    string `json:"branch"`
	Destroyed bool   `json:"destroyed"`
	Reason    string `json:"reason,omitempty"`
}

// DestroyReport summarizes a DestroyAllIdle call: which unclaimed
// worktrees were actually destroyed, and which were left alone (and
// why). Claimed worktrees (live or stale) are excluded entirely — they
// never appear in either list, since --all-idle only ever considers
// currently-unclaimed worktrees.
type DestroyReport struct {
	Destroyed []*DestroyResult `json:"destroyed"`
	Skipped   []*DestroyResult `json:"skipped"`
}

// ErrLiveClaim is returned by Destroy when the target worktree has a
// claim whose recorded PID is still alive. Destroy never overrides a
// live claim itself; the operator must release it first (`release
// --force`, if the claim is otherwise stuck).
var ErrLiveClaim = errors.New("canopy: worktree has a live claim; release it first (`canopy release --force --holder <holder>`)")

// destroyChecks runs destroy's two independent safety checks (dirty,
// unmerged) against wt and, if both pass (or are overridden by opts),
// removes the worktree from disk/git via gitutil.WorktreeRemove. It
// does not touch s.Worktrees — callers are responsible for removing the
// entry from the catalog on success. defaultBranch is the branch
// resolved once per Destroy/DestroyAllIdle call (see
// gitutil.DefaultBranch).
//
// It returns a DestroyResult describing the outcome, or an error only
// for unexpected failures (git/gitutil errors) — a refused-by-policy
// outcome (dirty/unmerged without the matching flag) is reported via
// DestroyResult.Reason, not an error, so callers can decide for
// themselves whether that should abort (single-path destroy) or just be
// reported and skipped (--all-idle).
func (p *Pool) destroyChecks(repoRoot, defaultBranch string, wt *state.Worktree, opts DestroyOptions) (*DestroyResult, error) {
	clean, err := gitutil.IsClean(wt.Path)
	if err != nil {
		return nil, fmt.Errorf("checking git status for %s: %w", wt.Path, err)
	}
	if !clean && !opts.IncludeDirty {
		return &DestroyResult{
			Path:   wt.Path,
			Branch: wt.Branch,
			Reason: "working tree has uncommitted changes; pass --include-dirty to destroy anyway",
		}, nil
	}

	merged, err := gitutil.IsMerged(repoRoot, wt.Branch, defaultBranch)
	if err != nil {
		return nil, fmt.Errorf("checking merge status for branch %s: %w", wt.Branch, err)
	}
	if !merged && !opts.IncludeUnlanded {
		return &DestroyResult{
			Path:   wt.Path,
			Branch: wt.Branch,
			Reason: fmt.Sprintf("branch %s is not merged into %s; pass --include-unlanded to destroy anyway", wt.Branch, defaultBranch),
		}, nil
	}

	if p.Hooks.PreDestroy != "" {
		holder := ""
		if wt.Claim != nil {
			holder = wt.Claim.Holder
		}
		runHook(p.Hooks.PreDestroy, wt.Path, holder)
	}

	if err := gitutil.WorktreeRemove(repoRoot, wt.Path, !clean); err != nil {
		return nil, fmt.Errorf("removing worktree %s: %w", wt.Path, err)
	}

	return &DestroyResult{Path: wt.Path, Branch: wt.Branch, Destroyed: true}, nil
}

// Destroy permanently removes the worktree at path from disk and git's
// worktree registration, and drops its catalog/claim entry from
// state.json — freeing pool capacity for good, unlike Release/Prune
// which only ever return a worktree to the pool for reuse.
//
// It refuses (returns ErrLiveClaim) if the worktree has a claim whose
// recorded PID is still alive; a stale claim does not block a
// directly-named path, since the dead process can no longer be using
// it, but --all-idle (DestroyAllIdle) is more conservative and skips
// any claimed worktree, live or stale (see its doc comment).
//
// It otherwise refuses an unmerged branch unless opts.IncludeUnlanded,
// and separately refuses a dirty working tree unless opts.IncludeDirty
// — both are required together when both conditions apply.
func (p *Pool) Destroy(path string, opts DestroyOptions) (*DestroyResult, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving path %s: %w", path, err)
	}

	defaultBranch, err := gitutil.DefaultBranch(p.RepoRoot)
	if err != nil {
		return nil, err
	}

	var result *DestroyResult
	err = state.WithLock(p.StatePath, func(s *state.State) error {
		var target *state.Worktree
		for _, wt := range s.Worktrees {
			wtAbs, err := filepath.Abs(wt.Path)
			if err != nil {
				return fmt.Errorf("resolving worktree path %s: %w", wt.Path, err)
			}
			if wtAbs == absPath {
				target = wt
				break
			}
		}
		if target == nil {
			return fmt.Errorf("canopy: %s is not a worktree canopy manages", path)
		}

		if target.Claim != nil && isAlive(target.Claim.PID) {
			return ErrLiveClaim
		}

		r, err := p.destroyChecks(p.RepoRoot, defaultBranch, target, opts)
		if err != nil {
			return err
		}
		if !r.Destroyed {
			return fmt.Errorf("canopy: %s", r.Reason)
		}

		s.Worktrees = removeWorktree(s.Worktrees, target)
		result = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DestroyAllIdle applies Destroy's same unmerged/dirty rules across
// every currently-unclaimed worktree in the pool. Any worktree with a
// claim — live or stale — is left entirely untouched and does not
// appear in the report; --all-idle is a bulk-cleanup convenience, not a
// way to sweep up claimed worktrees a path-by-path destroy would still
// refuse (live) or allow through (stale) case by case.
func (p *Pool) DestroyAllIdle(opts DestroyOptions) (*DestroyReport, error) {
	defaultBranch, err := gitutil.DefaultBranch(p.RepoRoot)
	if err != nil {
		return nil, err
	}

	report := &DestroyReport{
		Destroyed: []*DestroyResult{},
		Skipped:   []*DestroyResult{},
	}

	err = state.WithLock(p.StatePath, func(s *state.State) error {
		remaining := make([]*state.Worktree, 0, len(s.Worktrees))
		for _, wt := range s.Worktrees {
			if wt.Claim != nil {
				remaining = append(remaining, wt)
				continue
			}

			r, err := p.destroyChecks(p.RepoRoot, defaultBranch, wt, opts)
			if err != nil {
				return err
			}
			if r.Destroyed {
				report.Destroyed = append(report.Destroyed, r)
				continue // dropped from the catalog
			}
			report.Skipped = append(report.Skipped, r)
			remaining = append(remaining, wt)
		}
		s.Worktrees = remaining
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// removeWorktree returns list with target removed (by pointer
// identity), preserving order of the rest.
func removeWorktree(list []*state.Worktree, target *state.Worktree) []*state.Worktree {
	out := make([]*state.Worktree, 0, len(list)-1)
	for _, wt := range list {
		if wt != target {
			out = append(out, wt)
		}
	}
	return out
}

// ResolveWorktreePath validates that the given path is a known worktree
// in the pool. It performs a read-only lookup of state.json without
// taking any locks, making it suitable for commands that inspect but do
// not modify the pool (such as enter). It returns the matching worktree
// entry or an error if the path is not managed by this pool.
func (p *Pool) ResolveWorktreePath(path string) (*state.Worktree, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving path %s: %w", path, err)
	}

	s, err := state.Load(p.StatePath)
	if err != nil {
		return nil, err
	}

	for _, wt := range s.Worktrees {
		wtAbs, err := filepath.Abs(wt.Path)
		if err != nil {
			return nil, fmt.Errorf("resolving worktree path %s: %w", wt.Path, err)
		}
		if wtAbs == absPath {
			return wt, nil
		}
	}

	return nil, fmt.Errorf("canopy: %s is not a worktree canopy manages", path)
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
