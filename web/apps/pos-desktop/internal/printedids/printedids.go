// Package printedids remembers which order ids already produced a kitchen
// ticket, so the same order is never printed twice: not when the kitchen
// stream reconnects and replays its snapshot, and not after a restart.
//
// It is a bounded, recency-ordered set persisted to one small JSON file in the
// station's config directory. The bound (a few hundred ids) is what keeps the
// file tiny; an id old enough to be evicted belongs to an order the kitchen
// finished long ago and that the live snapshot no longer lists.
package printedids

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const fileName = "printed-kitchen-orders.json"

// DefaultMax is how many ids the station remembers.
const DefaultMax = 500

type fileFormat struct {
	IDs []string `json:"ids"`
}

// Set is safe for concurrent use.
type Set struct {
	path string
	max  int
	warn func(format string, args ...any)

	mu    sync.Mutex
	order []string // oldest first
	index map[string]struct{}
}

// Open loads the set stored in dir. A missing file is normal (first run). A
// corrupt one is reported through warn and treated as empty — losing the
// dedupe memory can at worst reprint recent tickets, which is better than
// refusing to start — and is overwritten by the next Add.
func Open(dir string, max int, warn func(format string, args ...any)) *Set {
	if max < 1 {
		max = DefaultMax
	}
	s := &Set{
		path:  filepath.Join(dir, fileName),
		max:   max,
		warn:  warn,
		index: make(map[string]struct{}),
	}

	data, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s
	case err != nil:
		s.warnf("printed orders: read %s: %v — starting empty", s.path, err)
		return s
	}

	var ff fileFormat
	if err := json.Unmarshal(data, &ff); err != nil {
		s.warnf("printed orders: %s is corrupt (%v) — starting empty", s.path, err)
		return s
	}
	for _, id := range ff.IDs {
		s.insert(id)
	}
	return s
}

func (s *Set) warnf(format string, args ...any) {
	if s.warn != nil {
		s.warn(format, args...)
	}
}

// Has reports whether id was recorded.
func (s *Set) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.index[id]
	return ok
}

// Add records id as the most recent entry, evicting the oldest beyond the
// bound, and persists the set. The in-memory set is updated even when
// persisting fails, so dedupe keeps working for the rest of the session; the
// error tells the caller that a restart may forget this id.
func (s *Set) Add(id string) error {
	if id == "" {
		return errors.New("printed orders: empty order id")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.insert(id)
	return s.persistLocked()
}

// insert moves or appends id to the recent end and trims. Caller holds mu (or
// is the constructor).
func (s *Set) insert(id string) {
	if id == "" {
		return
	}
	if _, ok := s.index[id]; ok {
		for i, existing := range s.order {
			if existing == id {
				s.order = append(s.order[:i], s.order[i+1:]...)
				break
			}
		}
	}
	s.order = append(s.order, id)
	s.index[id] = struct{}{}

	for len(s.order) > s.max {
		delete(s.index, s.order[0])
		s.order = s.order[1:]
	}
}

// persistLocked writes via a temp file + rename so a crash mid-write can never
// leave a truncated file behind.
func (s *Set) persistLocked() error {
	data, err := json.Marshal(fileFormat{IDs: s.order})
	if err != nil {
		return fmt.Errorf("printed orders: encode: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("printed orders: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, fileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("printed orders: temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("printed orders: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("printed orders: close: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("printed orders: chmod: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("printed orders: rename: %w", err)
	}
	return nil
}
