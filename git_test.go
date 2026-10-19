package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `diff --git a/a.go b/a.go
index 1..2 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@
 package a
-func old() {}
+func renamed() {}
 // end
@@ -10,2 +10,3 @@ func x
 x := 1
+y := 2
 z := 3`

func TestParseAndAnchor(t *testing.T) {
	fs := parseDiff(sample)
	if len(fs) != 1 || fs[0].Path != "a.go" {
		t.Fatalf("files: %+v", fs)
	}
	ls := fs[0].Lines
	if l := ls[2]; l.Kind != KDel || l.Old != 2 || l.New != 0 {
		t.Fatalf("del line: %+v", l)
	}
	if l := ls[7]; l.Kind != KAdd || l.New != 11 {
		t.Fatalf("add line: %+v", l)
	}

	// "+y := 2" still matches once committed (now a context line), even if it moved.
	th := &Thread{Path: "a.go", Line: 11, Lines: []string{"+y := 2"}}
	moved := parseDiff("diff --git a/a.go b/a.go\n+++ b/a.go\n@@ -20,2 +20,2 @@\n y := 2\n-z := 3\n+z := 4")
	if i := findAnchor(moved[0].Lines, th); i != 1 {
		t.Fatalf("anchor moved: got %d", i)
	}
	// A removed line is a different side: the comment is outdated.
	gone := parseDiff("diff --git a/a.go b/a.go\n+++ b/a.go\n@@ -1,1 +1,0 @@\n-y := 2")
	if i := findAnchor(gone[0].Lines, th); i != -1 {
		t.Fatalf("expected outdated, got %d", i)
	}
}

func TestBase(t *testing.T) {
	r := testRepo(t)
	run := func(args ...string) string {
		out, err := git(r.Root, nil, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	base := func(ref, want string) {
		t.Helper()
		if _, got, err := r.Base(ref); err != nil || got != want {
			t.Fatalf("Base(%q) on %s: %q %v, want %q", ref, r.Branch, got, err, want)
		}
	}
	init := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "unpushed")

	// main without an upstream: HEAD; with one: the upstream, so the unpushed commit shows.
	base("", "HEAD")
	run("remote", "add", "origin", r.Root)
	run("update-ref", "refs/remotes/origin/main", init)
	run("branch", "-q", "-u", "origin/main")
	base("", "origin/main")
	if c, _, _ := r.Base(""); c != init {
		t.Fatalf("Base is the upstream's commit: %s", c)
	}

	// A feature branch: main, unless origin/main is ahead of it.
	run("switch", "-q", "-c", "f")
	r.Branch = "f"
	run("update-ref", "refs/remotes/origin/main", "main")
	base("", "main")
	run("update-ref", "refs/heads/main", init)
	base("", "origin/main")
	base("dev", "dev")

	// agentpr.base wins over main; a missing branch falls back to main detection.
	run("config", "agentpr.base", "development")
	base("", "origin/main")
	run("branch", "development", init)
	base("", "development")
	run("update-ref", "refs/remotes/origin/development", "origin/main")
	base("", "origin/development")
	base("main", "main") // --base still overrides
	run("switch", "-q", "development")
	r.Branch = "development"
	if !r.State("HEAD", "").Default {
		t.Fatal("the configured base branch counts as Default")
	}
}

func TestFileStatus(t *testing.T) {
	fs := parseDiff(`diff --git a/n.go b/n.go
new file mode 100644
index 0000000..1
--- /dev/null
+++ b/n.go
@@ -0,0 +1,2 @@
+a
+b
diff --git a/d.go b/d.go
deleted file mode 100644
index 1..0000000
--- a/d.go
+++ /dev/null
@@ -1 +0,0 @@
-a
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
diff --git a/img.png b/img.png
new file mode 100644
index 0000000..1
Binary files /dev/null and b/img.png differ
` + sample[strings.Index(sample, "diff"):])
	want := []struct {
		path, status string
		add, del     int
	}{{"n.go", "new", 2, 0}, {"d.go", "deleted", 0, 1}, {"run.sh", "mode 100644 → 100755", 0, 0}, {"img.png", "new · binary", 0, 0}, {"a.go", "modified", 2, 1}}
	if len(fs) != len(want) {
		t.Fatalf("files: %+v", fs)
	}
	for i, w := range want {
		f := fs[i]
		if add, del := f.stats(); f.Path != w.path || f.Status != w.status || add != w.add || del != w.del {
			t.Errorf("file %d: %s %q +%d -%d, want %+v", i, f.Path, f.Status, add, del, w)
		}
		if len(f.Lines) > 0 && f.Lines[0].Kind != KHunk {
			t.Errorf("%s: meta lines must fold into the header, got %+v", f.Path, f.Lines[0])
		}
	}
}

func TestOpenRepo(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	main, wt, _ := testWorktree(t)
	os.MkdirAll(filepath.Join(main.Root, "sub"), 0o755)
	r, err := openRepo(filepath.Join(main.Root, "sub"))
	if err != nil || r.Root != main.Root || r.Branch != "main" || r.GitDir != main.GitDir {
		t.Fatalf("main: %+v %v", r, err)
	}
	if r, err := openRepo(wt.Root); err != nil || r.Branch != "feat" || r.GitDir != main.GitDir {
		t.Fatalf("worktree: %+v %v", r, err)
	}
	git(main.Root, nil, "switch", "-q", "--detach")
	if r, _ := openRepo(main.Root); r.Branch != "HEAD" {
		t.Fatalf("detached: %q", r.Branch)
	}
	if _, err := openRepo(t.TempDir()); err == nil {
		t.Fatal("non-repo accepted")
	}
}

func TestBaseEdges(t *testing.T) {
	// No commits yet.
	empty := t.TempDir()
	git(empty, nil, "init", "-q", "-b", "main")
	e := &Repo{Root: empty, Branch: "main"}
	if b, ref, err := e.Base(""); err != nil || b != emptyTree || ref != "empty tree" {
		t.Fatalf("empty: %q %q %v", b, ref, err)
	}
	if _, _, err := e.Base("nope"); err == nil {
		t.Fatal("empty repo, invalid ref accepted")
	}

	r := testRepo(t)
	r.Branch = "feat"
	git(r.Root, nil, "switch", "-q", "feat")
	head, _ := git(r.Root, nil, "rev-parse", "HEAD")
	if b, ref, err := r.Base("dev"); err != nil || b != head || ref != "dev" {
		t.Fatalf("ref: %q %q %v", b, ref, err)
	}
	if _, ref, err := r.Base(""); err != nil || ref != "main" {
		t.Fatalf("default: %q %v", ref, err)
	}
	if _, _, err := r.Base("nope"); err == nil {
		t.Fatal("invalid ref accepted")
	}

	// A root commit of its own shares no history.
	orphan, _ := git(r.Root, nil, "commit-tree", "-m", "x", emptyTree)
	git(r.Root, nil, "branch", "orphan", orphan)
	if _, _, err := r.Base("orphan"); err == nil || !strings.Contains(err.Error(), "shares no history") {
		t.Fatalf("orphan: %v", err)
	}
}

func TestPush(t *testing.T) {
	_, wt, origin := testWorktree(t)
	git(wt.Root, nil, "commit", "-q", "--allow-empty", "-m", "more")
	if err := wt.Push(); err != nil {
		t.Fatal(err)
	}
	local, _ := git(wt.Root, nil, "rev-parse", "HEAD")
	if remote, _ := git(origin, nil, "rev-parse", "feat"); remote != local {
		t.Fatalf("origin has %q, want %q", remote, local)
	}
	git(wt.Root, nil, "remote", "set-url", "origin", filepath.Join(origin, "gone"))
	if err := wt.Push(); err == nil {
		t.Fatal("push to a missing remote succeeded")
	}
}

func TestGh(t *testing.T) {
	r := testRepo(t)
	noTools(t)
	if _, err := gh(r.Root, "pr", "view"); !errors.As(err, new(noPR)) {
		t.Fatalf("missing gh: %v", err)
	}
	log := fakeTool(t, "gh", `echo "  out  "; [ "$GH_PROMPT_DISABLED" = 1 ]`)
	if out, err := gh(r.Root, "pr", "view"); err != nil || out != "out" {
		t.Fatalf("ok: %q %v", out, err)
	}
	if calls(log)[0] != "pr view" {
		t.Fatalf("calls: %q", calls(log))
	}
	fakeTool(t, "gh", "echo 'please run: gh auth login' >&2; exit 4")
	if _, err := gh(r.Root, "pr", "view"); !errors.As(err, new(noPR)) {
		t.Fatalf("logged out: %v", err)
	}
	fakeTool(t, "gh", "echo oops >&2; exit 1")
	if _, err := gh(r.Root, "pr", "view"); err == nil || errors.As(err, new(noPR)) || err.Error() != "gh pr view: oops" {
		t.Fatalf("other failure: %v", err)
	}
}

func TestRepoPR(t *testing.T) {
	r := testRepo(t)
	r.Branch = "feat"
	log := fakeTool(t, "gh", `echo '{"number":4,"url":"https://x/pull/4","state":"OPEN","baseRefName":"main"}'`)
	pr, err := r.PR()
	if err != nil || *pr != (PR{4, "https://x/pull/4", "OPEN", "main"}) {
		t.Fatalf("got %+v %v", pr, err)
	}
	if got := calls(log)[0]; got != "pr view feat --json number,url,state,baseRefName" {
		t.Fatalf("args: %q", got)
	}
	fakeTool(t, "gh", `echo 'no pull requests found for branch "feat"' >&2; exit 1`)
	if pr, err := r.PR(); pr != nil || err != nil {
		t.Fatalf("none: %+v %v", pr, err)
	}
	fakeTool(t, "gh", "echo network >&2; exit 1")
	if _, err := r.PR(); err == nil {
		t.Fatal("failure swallowed")
	}
	fakeTool(t, "gh", "echo not json")
	if _, err := r.PR(); err == nil {
		t.Fatal("bad json accepted")
	}
}

func TestCreatePR(t *testing.T) {
	_, wt, origin := testWorktree(t)
	log := fakeTool(t, "gh", `echo "Creating pull request"; echo https://x/pull/5`)
	url, err := wt.CreatePR("main", "title", "body")
	if err != nil || url != "https://x/pull/5" {
		t.Fatalf("got %q %v", url, err)
	}
	if got := calls(log)[0]; got != "pr create --base main --head feat --title title --body body" {
		t.Fatalf("args: %q", got)
	}
	git(wt.Root, nil, "remote", "set-url", "origin", filepath.Join(origin, "gone"))
	if _, err := wt.CreatePR("main", "t", "b"); err == nil {
		t.Fatal("push failure swallowed")
	}
}

func TestMergePR(t *testing.T) {
	r := testRepo(t)
	log := fakeTool(t, "gh", "")
	if err := r.MergePR(PR{Number: 3, Base: "main"}, "main", "squash"); err != nil {
		t.Fatal(err)
	}
	if got := calls(log); len(got) != 1 || got[0] != "pr merge 3 --squash" {
		t.Fatalf("same base: %q", got)
	}
	log = fakeTool(t, "gh", "")
	if err := r.MergePR(PR{Number: 3, Base: "main"}, "dev", "merge"); err != nil {
		t.Fatal(err)
	}
	if got := calls(log); len(got) != 2 || got[0] != "pr edit 3 --base dev" || got[1] != "pr merge 3 --merge" {
		t.Fatalf("retarget: %q", got)
	}
	log = fakeTool(t, "gh", `[ "$2" = edit ] && { echo denied >&2; exit 1; }`)
	if err := r.MergePR(PR{Number: 3, Base: "main"}, "dev", "merge"); err == nil || hasCall(log, "pr merge") {
		t.Fatalf("retarget failure: %v %q", err, calls(log))
	}
}
