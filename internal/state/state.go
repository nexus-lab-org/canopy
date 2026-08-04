// Package state manages canopy's on-disk pool state: a single JSON file
// at .git/canopy/state.json holding the worktree catalog and their claim
// records. Every read-modify-write cycle against that file is wrapped in
// a whole-file flock so concurrent canopy invocations never corrupt it.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// ErrAlreadyInitialized is returned by Init when state.json already exists.
var ErrAlreadyInitialized = errors.New("canopy: already initialized")

// ErrNotInitialized is returned by Load when state.json does not exist yet.
var ErrNotInitialized = errors.New("canopy: not initialized (run `canopy init` first)")

// Claim records who holds a worktree, the PID associated with that
// holder, and when the claim was made.
type Claim struct {
	Holder    string    `json:"holder"`
	PID       int       `json:"pid"`
	ClaimedAt time.Time `json:"claimed_at"`
}

// Worktree is one entry in the pool's catalog: a path/branch pair
// managed by canopy, plus its current claim (nil when free).
type Worktree struct {
	Path      string    `json:"path"`
	Branch    string    `json:"branch"`
	CreatedAt time.Time `json:"created_at"`
	Claim     *Claim    `json:"claim,omitempty"`
}

// State is the full contents of state.json.
type State struct {
	Version   int         `json:"version"`
	Worktrees []*Worktree `json:"worktrees"`
}

const currentVersion = 1

// Dir returns the canopy state directory for a repo, given its
// (common) .git directory.
func Dir(gitCommonDir string) string {
	return filepath.Join(gitCommonDir, "canopy")
}

// Path returns the state.json path for a repo, given its (common) .git
// directory.
func Path(gitCommonDir string) string {
	return filepath.Join(Dir(gitCommonDir), "state.json")
}

// Init creates .git/canopy/state.json with an empty pool. It returns
// ErrAlreadyInitialized if the file already exists.
func Init(gitCommonDir string) (string, error) {
	dir := Dir(gitCommonDir)
	path := Path(gitCommonDir)

	if _, err := os.Stat(path); err == nil {
		return path, ErrAlreadyInitialized
	} else if !os.IsNotExist(err) {
		return path, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return path, fmt.Errorf("creating %s: %w", dir, err)
	}

	empty := &State{Version: currentVersion, Worktrees: []*Worktree{}}
	if err := write(path, empty); err != nil {
		return path, err
	}
	return path, nil
}

// Load reads and parses state.json. It returns ErrNotInitialized if the
// file does not exist.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotInitialized
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &s, nil
}

// write atomically writes s to path (temp file + rename, so a reader
// never observes a partially-written file).
func write(path string, s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.json.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed away

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// WithLock takes an exclusive whole-file flock on path (state.json),
// loads the current state, runs fn against it, and — if fn returns a
// nil error — writes the (possibly mutated) state back before releasing
// the lock. This is the sole concurrency-safety mechanism for canopy's
// state file: every claim/release/prune/destroy bookkeeping update must
// go through it.
func WithLock(path string, fn func(*State) error) error {
	lockPath := path + ".lock"
	fl := flock.New(lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("locking %s: %w", lockPath, err)
	}
	defer fl.Unlock()

	s, err := Load(path)
	if err != nil {
		return err
	}

	if err := fn(s); err != nil {
		return err
	}

	return write(path, s)
}
