package store

import (
	"os"
	"testing"
)

func TestInterruptedWritesLeaveNamesFree(t *testing.T) {
	s := New(t.TempDir())
	create := func() {
		t.Helper()
		release, err := s.Create(Agent{Name: "a"})
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	create()
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSeen("a", "laptop", 1); err == nil || s.Exists("a") {
		t.Fatalf("MarkSeen brought back a deleted agent: %v", err)
	}
	create()
	for _, leftover := range []string{".new-b", ".del-a"} {
		if err := os.MkdirAll(s.Path(leftover, "stale"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	agents, unreadable, err := s.List()
	if err != nil || len(agents) != 1 || len(unreadable) != 0 {
		t.Fatalf("agents = %+v, unreadable = %+v, err = %v", agents, unreadable, err)
	}
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if s.Exists("a") {
		t.Fatal("the name is still taken")
	}
	create()
}
