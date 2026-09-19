package printedids

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newSet(t *testing.T, dir string, max int) (*Set, *[]string) {
	t.Helper()
	var warns []string
	s := Open(dir, max, func(format string, args ...any) { warns = append(warns, fmt.Sprintf(format, args...)) })
	return s, &warns
}

func TestSet_AddHas(t *testing.T) {
	s, _ := newSet(t, t.TempDir(), 10)
	if s.Has("a") {
		t.Fatal("empty set reports a member")
	}
	if err := s.Add("a"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !s.Has("a") || s.Has("b") {
		t.Fatal("membership wrong after Add")
	}
}

func TestSet_RejectsEmptyID(t *testing.T) {
	s, _ := newSet(t, t.TempDir(), 10)
	if err := s.Add(""); err == nil {
		t.Fatal("Add(\"\"): want error")
	}
	if s.Has("") {
		t.Fatal("empty id must never be a member")
	}
}

func TestSet_PersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, _ := newSet(t, dir, 10)
	for _, id := range []string{"a", "b", "c"} {
		if err := s.Add(id); err != nil {
			t.Fatalf("Add(%s): %v", id, err)
		}
	}

	reopened, warns := newSet(t, dir, 10)
	for _, id := range []string{"a", "b", "c"} {
		if !reopened.Has(id) {
			t.Fatalf("id %q lost across reopen", id)
		}
	}
	if len(*warns) != 0 {
		t.Fatalf("unexpected warnings on a clean reopen: %v", *warns)
	}
}

func TestSet_TrimsOldestBeyondMax(t *testing.T) {
	dir := t.TempDir()
	s, _ := newSet(t, dir, 3)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := s.Add(id); err != nil {
			t.Fatalf("Add(%s): %v", id, err)
		}
	}
	for id, want := range map[string]bool{"a": false, "b": false, "c": true, "d": true, "e": true} {
		if s.Has(id) != want {
			t.Fatalf("Has(%q) = %v, want %v", id, !want, want)
		}
	}

	reopened, _ := newSet(t, dir, 3)
	if reopened.Has("b") || !reopened.Has("e") {
		t.Fatal("trim was not persisted")
	}
}

func TestSet_ReAddRefreshesRecency(t *testing.T) {
	s, _ := newSet(t, t.TempDir(), 3)
	for _, id := range []string{"a", "b", "c"} {
		_ = s.Add(id)
	}
	_ = s.Add("a") // a is now the most recent; b is the oldest
	_ = s.Add("d")
	if !s.Has("a") || s.Has("b") {
		t.Fatal("re-adding a must protect it from the next trim; b should have been evicted")
	}
}

func TestSet_LoadTrimsOversizedFileToMax(t *testing.T) {
	dir := t.TempDir()
	big, _ := newSet(t, dir, 100)
	for i := 0; i < 10; i++ {
		_ = big.Add(fmt.Sprintf("id-%d", i))
	}
	small, _ := newSet(t, dir, 4)
	if small.Has("id-5") || !small.Has("id-6") || !small.Has("id-9") {
		t.Fatal("oversized file must keep only the newest max entries")
	}
}

func TestSet_CorruptFileWarnsAndStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, warns := newSet(t, dir, 10)
	if len(*warns) == 0 {
		t.Fatal("a corrupt file must be reported, not silently ignored")
	}
	if s.Has("anything") {
		t.Fatal("corrupt file must yield an empty set")
	}
	if err := s.Add("a"); err != nil {
		t.Fatalf("Add after corrupt load: %v", err)
	}
	reopened, again := newSet(t, dir, 10)
	if !reopened.Has("a") || len(*again) != 0 {
		t.Fatal("the corrupt file must be replaced by a valid one on the next write")
	}
}

func TestSet_CreatesMissingDirAndUses0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "cfg")
	s, _ := newSet(t, dir, 10)
	if err := s.Add("a"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %o, want 600", perm)
	}
}

func TestSet_PersistFailureKeepsMemoryAndReturnsError(t *testing.T) {
	dir := t.TempDir()
	// A regular file where the directory should be makes every write fail.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := newSet(t, filepath.Join(blocker, "sub"), 10)
	if err := s.Add("a"); err == nil {
		t.Fatal("Add: want persistence error")
	}
	if !s.Has("a") {
		t.Fatal("the in-memory set must still dedupe when the disk write fails")
	}
}

func TestSet_ConcurrentAdds(t *testing.T) {
	dir := t.TempDir()
	s, _ := newSet(t, dir, 50)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Add(fmt.Sprintf("id-%d", i))
			_ = s.Has("id-0")
		}()
	}
	wg.Wait()
	reopened, warns := newSet(t, dir, 50)
	if len(*warns) != 0 {
		t.Fatalf("file corrupted by concurrent writes: %v", *warns)
	}
	for i := 0; i < 20; i++ {
		if !reopened.Has(fmt.Sprintf("id-%d", i)) {
			t.Fatalf("id-%d missing", i)
		}
	}
}
