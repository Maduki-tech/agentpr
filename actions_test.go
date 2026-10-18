package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestQuick(t *testing.T) {
	open, merged := &PR{Number: 7, State: "OPEN"}, &PR{Number: 7, State: "MERGED"}
	for _, c := range []struct {
		s    GitState
		want string
		a    act
	}{
		{GitState{Branch: "HEAD", Dirty: true}, "Commit", aNone},
		{GitState{Branch: "f", Dirty: true}, "Commit", aCommit},
		{GitState{Branch: "f", Dirty: true, Remote: true}, "Commit, push & create PR", aCommitPushPR},
		{GitState{Branch: "main", Default: true, Dirty: true, Remote: true}, "Commit, push & create PR", aCommitPushPR},
		{GitState{Branch: "f", Dirty: true, Remote: true, PR: open}, "Commit & push", aCommitPush},
		{GitState{Branch: "f", Remote: true, Behind: 1}, "Pull", aNone},
		{GitState{Branch: "f", Remote: true, Ahead: 1, Delta: 1}, "Push & create PR", aPR},
		{GitState{Branch: "f", Remote: true, Ahead: 1, PR: open}, "Push", aPush},
		{GitState{Branch: "f", Remote: true, Delta: 1, PR: open}, "Merge PR", aMerge},
		{GitState{Branch: "f", Remote: true, Delta: 1, PR: merged}, "Merged", aNone},
		{GitState{Branch: "f", Remote: true, Delta: 1}, "Create PR", aPR},
		{GitState{Branch: "main", Default: true, Remote: true}, "Commit", aNone},
	} {
		if got, a, _ := quick(c.s); got != c.want || a != c.a {
			t.Errorf("%+v: got %q/%d, want %q/%d", c.s, got, a, c.want, c.a)
		}
	}
}

func TestParseBranches(t *testing.T) {
	got := parseBranches("refs/heads/main\nrefs/heads/feat/x\nrefs/remotes/origin/HEAD\nrefs/remotes/origin/main\nrefs/remotes/origin/dev")
	if want := []string{"main", "feat/x", "dev"}; !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}

func TestMenu(t *testing.T) {
	m := newModel(&Repo{Branch: "f"}, "")
	m.w, m.h, m.d.state = 100, 20, &State{}
	m.d.git = GitState{Branch: "f", Remote: true, Ahead: 1, Delta: 1}
	key := func(s string) { m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }

	key("C")
	if m.mode != mMenu || m.menu.sel != 0 {
		t.Fatalf("menu opens: mode=%d", m.mode)
	}
	key("k")
	if m.menu.sel != 4 {
		t.Fatalf("k wraps: sel=%d", m.menu.sel)
	}
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != mMenu || m.msg == "" {
		t.Fatalf("View PR without a PR is disabled: mode=%d msg=%q", m.mode, m.msg)
	}
	// Hover then click "Push".
	x, y, _, _ := m.menuBox()
	m.mouse(tea.MouseMsg{X: x + 2, Y: y + 1, Action: tea.MouseActionMotion})
	if m.menu.sel != 1 {
		t.Fatalf("hover selects: sel=%d", m.menu.sel)
	}
	m.mouse(tea.MouseMsg{X: x + 2, Y: y + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.mode != mNormal || m.busy != "pushing…" || m.cmd == nil {
		t.Fatalf("click pushes: mode=%d busy=%q", m.mode, m.busy)
	}

	// The status-line button runs the quick action; its ▾ opens the menu.
	m.busy, m.cmd = "", nil
	m.status()
	m.mouse(tea.MouseMsg{X: m.caretX + 1, Y: m.h - 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.mode != mMenu {
		t.Fatalf("caret opens menu: mode=%d", m.mode)
	}
	m.mouse(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.mode != mNormal {
		t.Fatalf("click outside closes: mode=%d", m.mode)
	}
}

func testRepo(t *testing.T) *Repo {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"branch", "dev"},
		{"branch", "feat"},
		{"config", "user.name", "t"},
		{"config", "user.email", "t@t"},
	} {
		if _, err := git(dir, nil, args...); err != nil {
			t.Fatal(err)
		}
	}
	return &Repo{Root: dir, GitDir: filepath.Join(dir, ".git"), Branch: "main"}
}

func TestCommitOnMain(t *testing.T) {
	r := testRepo(t)
	os.WriteFile(filepath.Join(r.Root, "new.txt"), []byte("hi\n"), 0o644)
	m := newModel(r, "")
	m.w, m.h, m.d.state = 100, 20, &State{}
	m.d.git = GitState{Branch: "main", Default: true, Dirty: true}
	typ := func(s string) {
		m.input.SetValue(s)
		m.key(tea.KeyMsg{Type: tea.KeyEnter})
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.mode != mGit || !strings.Contains(m.input.Placeholder, "new branch") {
		t.Fatalf("asks for a branch: mode=%d %q", m.mode, m.input.Placeholder)
	}
	typ("fix-it")
	typ("Add new.txt")
	if m.busy != "committing…" {
		t.Fatalf("commits: busy=%q msg=%q", m.busy, m.msg)
	}
	m.done(m.cmd().(gitDone))
	if m.msg != "committed" || m.repo.Branch != "fix-it" {
		t.Fatalf("after commit: msg=%q branch=%q", m.msg, m.repo.Branch)
	}
	if out, _ := git(r.Root, nil, "log", "-1", "--format=%s", "--name-only"); out != "Add new.txt\n\nnew.txt" {
		t.Fatalf("commit: %q", out)
	}
}

func TestMergeFlow(t *testing.T) {
	r := testRepo(t)
	r.Branch = "feat"
	m := newModel(r, "")
	m.w, m.h, m.d.state = 100, 20, &State{}
	m.pr = &PR{Number: 3, State: "OPEN", Base: "dev"}

	m.run(aMerge)
	if m.mode != mMenu || m.menu.items[m.menu.sel].label != "dev" || slices.ContainsFunc(m.menu.items, func(i item) bool { return i.label == "feat" }) {
		t.Fatalf("target picker: %+v sel=%d", m.menu.items, m.menu.sel)
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.menu.title != "Merge method" || m.menu.items[m.menu.sel].label != "squash" {
		t.Fatalf("method picker: %q sel=%d", m.menu.title, m.menu.sel)
	}
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.busy != "merging…" || m.cmd == nil {
		t.Fatalf("merges: busy=%q", m.busy)
	}
}
