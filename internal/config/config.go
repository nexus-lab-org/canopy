// Package config loads canopy's two-tier TOML configuration: a
// checked-in repo-level canopy.toml holding shared project policy (pool
// max, branch-naming scheme), and an optional user-level
// ~/.config/canopy/config.toml holding machine-local defaults (worktree
// base directory) plus post_create/pre_destroy hooks.
//
// The split — and the rule that hooks are user-level only — mirrors
// treehouse's own precedent (see wayfinder/tickets/008-config-file-design.md):
// pool size and naming conventions are project policy that should be
// consistent for every agent session against a repo, so they belong in
// version control; hooks execute arbitrary shell commands, so allowing a
// repo-level hook would mean cloning a repo and running a canopy command
// in it could run code the repo author chose — a supply-chain risk. A
// hook is something you configure for repos you trust, never something a
// repo can impose on you by being checked out. Repo-level canopy.toml is
// therefore never permitted to define hooks; if it tries to, Load drops
// them and prints a warning rather than honoring them.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Hooks holds shell commands to run around pool operations. Only ever
// populated from the user-level config file — see the package doc.
type Hooks struct {
	// PostCreate runs (via `sh -c`) after a successful `claim` hands out
	// a worktree, with CANOPY_WORKTREE_PATH and CANOPY_HOLDER set in its
	// environment.
	PostCreate string
	// PreDestroy runs (via `sh -c`) before `destroy` removes a worktree
	// from disk, with CANOPY_WORKTREE_PATH and CANOPY_HOLDER (empty for
	// an unclaimed worktree) set in its environment.
	PreDestroy string
}

// Config is the merged, resolved view of repo-level and user-level
// config that the rest of canopy consumes. Zero values mean "not
// configured" — callers apply their own defaults.
type Config struct {
	// Max is the pool's configured cap (claimed + idle worktrees) from
	// repo-level canopy.toml. 0 means unconfigured/unlimited.
	Max int
	// BranchNaming is the template for auto-created branch names, e.g.
	// "canopy/{holder}". "" means unconfigured; callers fall back to
	// their own default.
	BranchNaming string
	// WorktreeBaseDir is the directory new worktrees are created under,
	// from user-level config.toml. May start with "~", which Load
	// expands via os.UserHomeDir(). "" means unconfigured; callers fall
	// back to their own default.
	WorktreeBaseDir string
	// Hooks is always sourced from the user-level file only.
	Hooks Hooks
}

// fileConfig is the raw shape of either TOML file. Pointer fields
// distinguish "absent" from "explicitly zero" so merging can tell
// whether a lower-precedence file's value should show through.
type fileConfig struct {
	Max             *int         `toml:"max"`
	BranchNaming    *string      `toml:"branch_naming"`
	WorktreeBaseDir *string      `toml:"worktree_base_dir"`
	Hooks           *hooksConfig `toml:"hooks"`
}

type hooksConfig struct {
	PostCreate string `toml:"post_create"`
	PreDestroy string `toml:"pre_destroy"`
}

// RepoFileName is the checked-in repo-level config file's name, expected
// at the repo root.
const RepoFileName = "canopy.toml"

// Load reads <repoRoot>/canopy.toml (repo-level, if present) and
// ~/.config/canopy/config.toml (user-level, if present), merging them
// with repo-level values taking precedence over user-level values for
// any key set in both. Neither file is required to exist; a missing
// file is treated as empty configuration, not an error.
//
// Hooks found in the repo-level file are never honored: Load strips them
// and prints a warning to stderr (see the package doc for why). Hooks
// are only ever taken from the user-level file.
func Load(repoRoot string) (*Config, error) {
	userPath, err := userConfigPath()
	if err != nil {
		return nil, err
	}

	userCfg, err := loadFile(userPath)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", userPath, err)
	}

	repoPath := filepath.Join(repoRoot, RepoFileName)
	repoCfg, err := loadFile(repoPath)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", repoPath, err)
	}
	if repoCfg != nil && repoCfg.Hooks != nil {
		fmt.Fprintf(os.Stderr, "canopy: warning: %s defines [hooks], but hooks are only honored in user-level config (%s); ignoring\n", repoPath, userPath)
		repoCfg.Hooks = nil
	}

	cfg := &Config{}
	applyFileConfig(cfg, userCfg)
	applyFileConfig(cfg, repoCfg) // repo-level takes precedence

	if cfg.WorktreeBaseDir != "" {
		expanded, err := expandHome(cfg.WorktreeBaseDir)
		if err != nil {
			return nil, err
		}
		cfg.WorktreeBaseDir = expanded
	}

	return cfg, nil
}

// applyFileConfig overlays fc's set fields onto cfg. A nil fc is a no-op
// (file absent). Hooks are only ever taken from a file whose Hooks field
// survived to this point — Load has already stripped repo-level hooks
// before calling this for the repo file.
func applyFileConfig(cfg *Config, fc *fileConfig) {
	if fc == nil {
		return
	}
	if fc.Max != nil {
		cfg.Max = *fc.Max
	}
	if fc.BranchNaming != nil {
		cfg.BranchNaming = *fc.BranchNaming
	}
	if fc.WorktreeBaseDir != nil {
		cfg.WorktreeBaseDir = *fc.WorktreeBaseDir
	}
	if fc.Hooks != nil {
		cfg.Hooks = Hooks{
			PostCreate: fc.Hooks.PostCreate,
			PreDestroy: fc.Hooks.PreDestroy,
		}
	}
}

// loadFile parses the TOML file at path, returning nil (not an error) if
// it does not exist.
func loadFile(path string) (*fileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var fc fileConfig
	if _, err := toml.Decode(string(data), &fc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &fc, nil
}

// userConfigPath returns ~/.config/canopy/config.toml, resolving the
// home directory via os.UserHomeDir() (which honors $HOME, making it
// straightforward for tests to isolate).
func userConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config", "canopy", "config.toml"), nil
}

// expandHome expands a leading "~" (or "~/...") in path to the current
// user's home directory, via os.UserHomeDir().
func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}
