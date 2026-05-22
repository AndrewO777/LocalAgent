package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andrew/localagent/internal/agent"
)

func newTestStore(t *testing.T) *FileStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions")
	s, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s
}

func sampleStored(id string) StoredSession {
	return StoredSession{
		ID:        id,
		Goal:      "test",
		Model:     "m",
		Workdir:   "/wd",
		StartedAt: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC),
		EndedAt:   time.Date(2025, 1, 2, 3, 5, 0, 0, time.UTC),
		Status:    "finished",
		Events:    []agent.Event{{Type: agent.EventDone, Reason: "finished"}},
	}
}

func TestFileStore_SaveGetRoundtrip(t *testing.T) {
	s := newTestStore(t)
	in := sampleStored("aaaa1111")
	if err := s.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := s.Get("aaaa1111")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != in.ID || got.Goal != in.Goal || got.Status != in.Status {
		t.Errorf("roundtrip mismatch: %+v vs %+v", got, in)
	}
	if len(got.Events) != 1 {
		t.Errorf("event count: got %d want 1", len(got.Events))
	}
}

func TestFileStore_GetNotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get("nopesuch1"); err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestFileStore_GetRejectsInvalidID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Get("../etc/passwd"); err == nil {
		t.Fatal("expected error for malicious id")
	}
}

func TestFileStore_DeleteSucceedsThenGetFails(t *testing.T) {
	s := newTestStore(t)
	in := sampleStored("bbbb2222")
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("bbbb2222"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("bbbb2222"); err == nil {
		t.Fatal("expected Get to fail after Delete")
	}
}

func TestFileStore_DeleteMissingIsOK(t *testing.T) {
	s := newTestStore(t)
	if err := s.Delete("never-existed"); err != nil {
		t.Fatalf("Delete on missing should be no-op, got %v", err)
	}
}

func TestFileStore_ListSortedNewestFirst(t *testing.T) {
	s := newTestStore(t)
	for i, id := range []string{"aaaa1111", "bbbb2222", "cccc3333"} {
		in := sampleStored(id)
		in.StartedAt = time.Date(2025, 1, 1+i, 0, 0, 0, 0, time.UTC)
		if err := s.Save(in); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(list))
	}
	if list[0].ID != "cccc3333" || list[1].ID != "bbbb2222" || list[2].ID != "aaaa1111" {
		t.Errorf("order wrong: %v", []string{list[0].ID, list[1].ID, list[2].ID})
	}
}

func TestFileStore_ListSkipsCorruptFiles(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(sampleStored("good1234")); err != nil {
		t.Fatal(err)
	}
	// Plant a malformed .json file alongside the good one.
	if err := os.WriteFile(filepath.Join(s.dir, "bad5678.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	// Corrupt file is silently skipped; good one survives.
	if len(list) != 1 || list[0].ID != "good1234" {
		t.Errorf("expected only good session, got %+v", list)
	}
}

func TestFileStore_ListMissingDirReturnsNilNoError(t *testing.T) {
	// Construct a store pointing at a path that does NOT exist yet.
	dir := filepath.Join(t.TempDir(), "doesnt-exist-yet")
	s := &FileStore{dir: dir}
	list, err := s.List()
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if list != nil {
		t.Errorf("expected nil list, got %+v", list)
	}
}

func TestFileStore_ListIgnoresTmpFiles(t *testing.T) {
	s := newTestStore(t)
	if err := s.Save(sampleStored("good9999")); err != nil {
		t.Fatal(err)
	}
	// A leftover tmp file (e.g. from a crashed mid-write) should be ignored.
	if err := os.WriteFile(filepath.Join(s.dir, "abc12345.json.tmp"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session, got %d", len(list))
	}
}

func TestFileStore_SaveIsAtomic(t *testing.T) {
	// Save writes to <id>.json.tmp then renames. After Save returns, no
	// .tmp file should be left lying around.
	s := newTestStore(t)
	if err := s.Save(sampleStored("xxxx7777")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("found leftover tmp file: %s", e.Name())
		}
	}
}

// --- Manager tests --------------------------------------------------------

func TestManager_GetFromStoreWhenNotInMemory(t *testing.T) {
	mgr := NewManager(newTestStore(t))
	if err := mgr.store.Save(sampleStored("disk1234")); err != nil {
		t.Fatal(err)
	}
	sess, ok := mgr.Get("disk1234")
	if !ok {
		t.Fatal("expected to find session via disk fallback")
	}
	if sess.Goal != "test" {
		t.Errorf("goal: got %q want %q", sess.Goal, "test")
	}
	// After Get, the session is cached in memory.
	if _, ok := mgr.sessions["disk1234"]; !ok {
		t.Error("session should be cached after disk fetch")
	}
}

func TestManager_GetUnknownReturnsFalse(t *testing.T) {
	mgr := NewManager(newTestStore(t))
	if _, ok := mgr.Get("nope0000"); ok {
		t.Fatal("expected ok=false for unknown id")
	}
}

func TestManager_SummariesMergesMemoryAndDisk(t *testing.T) {
	mgr := NewManager(newTestStore(t))
	// Add one in memory.
	_ = mgr.Create("g1", "m", "", 0, "/wd", "", func() {})
	// Add one to disk that's not in memory.
	if err := mgr.store.Save(sampleStored("ondisk22")); err != nil {
		t.Fatal(err)
	}
	out := mgr.Summaries()
	if len(out) != 2 {
		t.Fatalf("expected 2 sessions, got %d (%+v)", len(out), out)
	}
}
