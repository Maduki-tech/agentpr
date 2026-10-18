package main

import (
	"errors"
	"fmt"
	"maps"
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
		// No GitHub remote, gh missing or logged out: never an action that needs gh.
		{GitState{Branch: "f", Dirty: true, Remote: true, NoPR: "x"}, "Commit & push", aCommitPush},
		{GitState{Branch: "f", Dirty: true, NoPR: "x"}, "Commit", aCommit},
		{GitState{Branch: "f", Remote: true, Ahead: 1, Delta: 1, NoPR: "x"}, "Push", aPush},
		{GitState{Branch: "f", Remote: true, Delta: 1, NoPR: "x"}, "Create PR", aNone},
	} {
		if got, a, _ := quick(c.s); got != c.want || a != c.a {
			t.Errorf("%+v: got %q/%d, want %q/%d", c.s, got, a, c.want, c.a)
		}
	}
}

func TestMenuNoPR(t *testing.T) {
	on := map[string]bool{}
	for _, it := range menuItems(GitState{Branch: "f", Dirty: true, Remote: true, Ahead: 1, Delta: 1, NoPR: "x", PR: &PR{State: "OPEN"}}) {
		on[it.label] = it.on
	}
	if want := map[string]bool{"Commit": true, "Push": true, "Create PR": false, "Merge PR": false, "View PR": false}; !maps.Equal(on, want) {
		t.Fatalf("got %v", on)
	}
}

func TestGhFail(t *testing.T) {
	args := []string{"pr", "view", "f"}
	for _, c := range []struct {
		stderr, want string
		noPR         bool
	}{
		{"none of the git remotes configured for this repository point to a known GitHub host. To tell gh about a new GitHub host, please use `gh auth login`", "origin isn't a GitHub remote: PRs unavailable", true},
		{"To get started with GitHub CLI, please run:  gh auth login\nAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.", "not logged in to GitHub, run: gh auth login", true},
		{"error connecting to api.github.com\n", "gh pr view: error connecting to api.github.com", false},
	} {
		err := ghFail(args, c.stderr)
		if _, ok := err.(noPR); err.Error() != c.want || ok != c.noPR {
			t.Errorf("%q: got %q noPR=%v", c.stderr, err, ok)
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
	m.w, m.h, m.d.state, m.rows = 100, 20, &State{}, []Row{{Kind: KTop}}
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

	// The toolbar's git button runs the quick action; its ▾ opens the menu.
	m.busy, m.cmd = "", nil
	if !clickTool(m, "C") || m.mode != mMenu {
		t.Fatalf("caret opens menu: mode=%d", m.mode)
	}
	m.mouse(tea.MouseMsg{X: 0, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
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
	var diff string
	defer func(f func(string, string, string) (string, error)) { runClaude = f }(runClaude)
	runClaude = func(dir, prompt, in string) (string, error) {
		diff = in
		return "```\nfeat: add new.txt\n```", nil
	}

	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.mode != mGit || !strings.Contains(m.input.Placeholder, "new branch") {
		t.Fatalf("asks for a branch: mode=%d %q", m.mode, m.input.Placeholder)
	}
	typ("fix-it")
	m.Update(m.cmd())
	// The draft sees the untracked file, staged first, and lands in the message box.
	if !strings.Contains(diff, "new.txt") || m.input.Value() != "feat: add new.txt" {
		t.Fatalf("draft: diff=%q input=%q", diff, m.input.Value())
	}
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.busy != "committing…" {
		t.Fatalf("commits: busy=%q msg=%q", m.busy, m.msg)
	}
	m.done(m.cmd().(gitDone))
	if m.msg != "committed" || m.repo.Branch != "fix-it" {
		t.Fatalf("after commit: msg=%q branch=%q", m.msg, m.repo.Branch)
	}
	if out, _ := git(r.Root, nil, "log", "-1", "--format=%s", "--name-only"); out != "feat: add new.txt\n\nnew.txt" {
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

func TestDraftFails(t *testing.T) {
	r := testRepo(t)
	r.Branch = "feat"
	m := newModel(r, "")
	m.w, m.h, m.d.state = 100, 20, &State{}
	defer func(f func(string, string, string) (string, error)) { runClaude = f }(runClaude)
	runClaude = func(dir, prompt, in string) (string, error) { return "", errors.New("claude not found") }

	m.commit(aCommit)
	m.Update(m.cmd())
	if m.mode != mGit || m.input.Value() != "" || m.msg != "claude not found" {
		t.Fatalf("falls back to an empty box: mode=%d input=%q msg=%q", m.mode, m.input.Value(), m.msg)
	}
	// ctrl+r asks again; a draft for a box that was closed meanwhile is dropped.
	runClaude = func(dir, prompt, in string) (string, error) { return "fix: retry", nil }
	m.key(tea.KeyMsg{Type: tea.KeyCtrlR})
	late := m.cmd
	m.Update(m.cmd())
	if m.input.Value() != "fix: retry" {
		t.Fatalf("ctrl+r redrafts: %q", m.input.Value())
	}
	m.key(tea.KeyMsg{Type: tea.KeyEsc})
	m.ask(mGit, "other", func(string) {})
	m.Update(late())
	if m.input.Value() != "" {
		t.Fatalf("stale draft landed: %q", m.input.Value())
	}
}

func TestPickBase(t *testing.T) {
	r := testRepo(t)
	r.Branch = "feat"
	m := newModel(r, "")
	m.w, m.h = 100, 20
	m.Update(m.load()()) // against main
	if m.d.baseRef != "main" {
		t.Fatalf("default base: %q %s", m.d.baseRef, m.msg)
	}

	if !clickTool(m, "b") || m.mode != mMenu || m.menu.items[m.menu.sel].label != "main" || slices.ContainsFunc(m.menu.items, func(i item) bool { return i.label == "feat" }) {
		t.Fatalf("base picker: mode=%d %+v", m.mode, m.menu)
	}
	m.key(tea.KeyMsg{Type: tea.KeyUp}) // "dev": the same commit as main, so only the name differs
	m.key(tea.KeyMsg{Type: tea.KeyEnter})
	if m.baseFlag != "dev" || m.cmd == nil {
		t.Fatalf("picks dev: %q", m.baseFlag)
	}
	d := m.cmd().(loaded)
	m.Update(d)
	if !d.once || m.d.baseRef != "dev" || !strings.Contains(m.rows[0].Text, "vs dev") {
		t.Fatalf("reloads against dev: once=%v ref=%q top=%q", d.once, m.d.baseRef, m.rows[0].Text)
	}
}

// testWorktree is testRepo with a bare origin and a linked worktree on the pushed branch "feat".
func testWorktree(t *testing.T) (main, wt *Repo, origin string) {
	t.Setenv("HERDR_ENV", "") // never the real herdr
	main = testRepo(t)
	origin, wt = t.TempDir(), &Repo{Root: filepath.Join(t.TempDir(), "wt"), GitDir: main.GitDir, Branch: "feat"}
	for _, c := range []struct {
		dir  string
		args []string
	}{
		{origin, []string{"init", "-q", "--bare"}},
		{main.Root, []string{"remote", "add", "origin", origin}},
		{main.Root, []string{"push", "-q", "origin", "main", "feat"}},
		{main.Root, []string{"worktree", "add", "-q", wt.Root, "feat"}},
	} {
		if _, err := git(c.dir, nil, c.args...); err != nil {
			t.Fatal(err)
		}
	}
	return main, wt, origin
}

func TestUnsaved(t *testing.T) {
	main, wt, _ := testWorktree(t)
	if main.Linked() || !wt.Linked() {
		t.Fatalf("linked: main=%v wt=%v", main.Linked(), wt.Linked())
	}
	lost := func() string {
		s, err := wt.Unsaved()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := lost(); s != "" {
		t.Fatalf("clean: %q", s)
	}
	os.WriteFile(filepath.Join(wt.Root, "a"), []byte("a"), 0o644)
	git(wt.Root, nil, "commit", "-q", "--allow-empty", "-m", "local")
	if s := lost(); s != "1 untracked file, 1 commit on no remote" {
		t.Fatalf("extras: %q", s)
	}
	git(wt.Root, nil, "add", "a")
	os.WriteFile(filepath.Join(wt.Root, "b"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(wt.Root, "c"), []byte("c"), 0o644)
	if s := lost(); s != "1 uncommitted change, 2 untracked files, 1 commit on no remote" {
		t.Fatalf("staged: %q", s)
	}
}

func TestRemoveSteps(t *testing.T) {
	r := Repo{Root: "/wt", Branch: "feat"}
	flat := func(ss []step) (out []string) {
		for _, s := range ss {
			out = append(out, s.dir+": "+strings.Join(s.cmd, " "))
		}
		return out
	}
	head := []string{"/wt: git push -q origin --delete feat", "/wt: git switch -q --detach", "/main: git branch -q -D feat"}
	if got, want := flat(removeSteps(r, "/main", "", false)), append(slices.Clone(head), "/main: git worktree remove /wt"); !slices.Equal(got, want) {
		t.Errorf("git: %q", got)
	}
	if got, want := flat(removeSteps(r, "/main", "w2", true)), append(slices.Clone(head), "/main: herdr worktree remove --workspace w2 --force"); !slices.Equal(got, want) {
		t.Errorf("herdr: %q", got)
	}
}

func TestRemoveAfterMerge(t *testing.T) {
	main, wt, origin := testWorktree(t)
	m := newModel(wt, "")
	m.w, m.h, m.d.state = 300, 20, &State{}

	// Unsaved work: ask, and n keeps everything.
	m.Update(merged{pr: 3, msg: "merged PR #3", lost: "1 untracked file"})
	if m.mode != mConfirm || !strings.Contains(m.status(), "loses 1 untracked file: remove anyway? (y/n)") {
		t.Fatalf("asks: mode=%d %q", m.mode, m.status())
	}
	m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.mode != mNormal || m.busy != "" || m.msg != "merged PR #3 · worktree kept" {
		t.Fatalf("keeps: busy=%q msg=%q", m.busy, m.msg)
	}

	// A failure keeps agentpr open with the error, and the worktree.
	git(main.Root, nil, "remote", "set-url", "origin", filepath.Join(origin, "gone"))
	_, cmd := m.Update(merged{pr: 3, msg: "merged PR #3"})
	if d, ok := cmd().(gitDone); !ok || !strings.Contains(fmt.Sprint(d.err), "merged #3, but removing the worktree failed") {
		t.Fatalf("failure: %+v", d)
	}
	if _, err := os.Stat(wt.Root); err != nil {
		t.Fatalf("worktree removed after a failure: %v", err)
	}
	git(main.Root, nil, "remote", "set-url", "origin", origin)

	_, cmd = m.Update(merged{pr: 3, msg: "merged PR #3"})
	if m.busy != "removing worktree…" {
		t.Fatalf("removes: busy=%q", m.busy)
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("quits: %+v", msg)
	}
	if _, err := os.Stat(wt.Root); !os.IsNotExist(err) {
		t.Fatalf("worktree still there: %v", err)
	}
	for dir, ref := range map[string]string{main.Root: "refs/heads/feat", origin: "refs/heads/feat"} {
		if out, _ := git(dir, nil, "for-each-ref", ref); out != "" {
			t.Fatalf("branch left in %s: %q", dir, out)
		}
	}
}
