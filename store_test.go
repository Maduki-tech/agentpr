package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWhere(t *testing.T) {
	for _, c := range []struct {
		t    Thread
		want string
	}{
		{Thread{}, "(whole changeset)"},
		{Thread{Path: "a.go"}, "a.go"},
		{Thread{Path: "a.go", Line: 4, Lines: []string{"+x"}}, "a.go:4"},
		{Thread{Path: "a.go", Line: 4, Lines: []string{"+x", "+y", "+z"}}, "a.go:4-6"},
	} {
		if got := c.t.Where(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.t, got, c.want)
		}
	}
}

func TestPendingAndLookup(t *testing.T) {
	th := &Thread{ID: "t1", Comments: []Comment{{Author: Reviewer}, {Author: Author}}}
	if th.Pending() {
		t.Fatal("no pending comment")
	}
	th.Comments[1].Pending = true
	if !th.Pending() {
		t.Fatal("pending comment missed")
	}
	s := &State{Threads: []*Thread{th}}
	if s.Thread("t1") != th || s.Thread("nope") != nil {
		t.Fatal("Thread lookup")
	}
}

func TestStatePathFlattensBranch(t *testing.T) {
	r := &Repo{GitDir: "/g", Branch: "feat/x/y"}
	if got, want := r.StatePath(), filepath.Join("/g", "agentpr", "feat__x__y.json"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "b.json")
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	_, err := updateState(path, func(s *State) error {
		s.NextID = 2
		s.Threads = []*Thread{{ID: "t1", Path: "a.go", Line: 3, Lines: []string{"+x"}, Outdated: true,
			Comments: []Comment{{Author: Reviewer, Body: "why?", Pending: true, At: at}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
	s, raw, err := loadState(path)
	if err != nil || len(raw) == 0 {
		t.Fatalf("load: %v, %d bytes", err, len(raw))
	}
	th := s.Thread("t1")
	if s.NextID != 2 || th == nil || th.Path != "a.go" || th.Line != 3 || th.Lines[0] != "+x" {
		t.Fatalf("round trip lost data: %+v", s)
	}
	if th.Outdated {
		t.Fatal("Outdated is derived and must not be stored")
	}
	if c := th.Comments[0]; c.Body != "why?" || !c.Pending || !c.At.Equal(at) {
		t.Fatalf("comment: %+v", c)
	}
}

func TestUpdateStateKeepsConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.json")
	add := func(id string) func(*State) error {
		return func(s *State) error { s.Threads = append(s.Threads, &Thread{ID: id}); return nil }
	}
	updateState(path, add("t1"))
	s, err := updateState(path, add("t2")) // reads t1 back from disk, as when the Author replied meanwhile
	if err != nil || len(s.Threads) != 2 {
		t.Fatalf("got %v, %v", s, err)
	}
}

func TestLoadStateMissingAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	s, raw, err := loadState(filepath.Join(dir, "none.json"))
	if err != nil || s == nil || len(s.Threads) != 0 || raw != nil {
		t.Fatalf("missing file: %v %v %v", s, raw, err)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	if _, _, err := loadState(bad); err == nil {
		t.Fatal("corrupt file must error")
	}
	if _, _, err := loadState(dir); err == nil { // a directory: ReadFile fails, not IsNotExist
		t.Fatal("unreadable path must error")
	}
}

func TestUpdateStateErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{"), 0o644)
	called := false
	if _, err := updateState(bad, func(*State) error { called = true; return nil }); err == nil || called {
		t.Fatalf("corrupt state: err=%v called=%v", err, called)
	}
	good := filepath.Join(dir, "good.json")
	boom := errors.New("boom")
	if _, err := updateState(good, func(*State) error { return boom }); err != boom {
		t.Fatalf("callback error not returned: %v", err)
	}
	if _, err := os.Stat(good); !os.IsNotExist(err) {
		t.Fatal("failed update must not write")
	}
	os.WriteFile(filepath.Join(dir, "file"), nil, 0o644)
	if _, err := updateState(filepath.Join(dir, "file", "x.json"), func(*State) error { return nil }); err == nil {
		t.Fatal("unwritable dir must error")
	}
}
