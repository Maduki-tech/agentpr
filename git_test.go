package main

import (
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
