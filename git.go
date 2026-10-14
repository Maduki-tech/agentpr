package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

type Repo struct {
	Root   string // worktree top level
	GitDir string // common git dir, shared by all worktrees
	Branch string
}

func git(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
	}
	return strings.TrimRight(string(out), "\n"), err
}

func openRepo() (*Repo, error) {
	root, err := git(".", nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	gitDir, err := git(root, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	branch, err := git(root, nil, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		branch = "HEAD" // detached
	}
	return &Repo{root, gitDir, branch}, nil
}

// Base: --base override, else merge-base with main/master on a feature branch, else HEAD.
func (r *Repo) Base(override string) (string, error) {
	if override != "" {
		return git(r.Root, nil, "rev-parse", "--verify", override+"^{commit}")
	}
	head, err := git(r.Root, nil, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return emptyTree, nil // no commits yet
	}
	if r.Branch != "main" && r.Branch != "master" {
		for _, m := range []string{"main", "master", "origin/main", "origin/master"} {
			if mb, err := git(r.Root, nil, "merge-base", "HEAD", m); err == nil {
				return mb, nil
			}
		}
	}
	return head, nil
}

// Snapshot writes the working tree (tracked + untracked, minus ignored) as a git tree
// via a throwaway index, so the real index is never touched.
func (r *Repo) Snapshot() (string, error) {
	f, err := os.CreateTemp("", "agentpr-index-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	f.Close()
	defer os.Remove(tmp)
	// Seed with the real index so unchanged files hit git's stat cache.
	idx, _ := git(r.Root, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if b, err := os.ReadFile(idx); err == nil {
		os.WriteFile(tmp, b, 0o600)
	} else {
		os.Remove(tmp) // git rejects an empty index file
	}
	env := []string{"GIT_INDEX_FILE=" + tmp}
	if _, err := git(r.Root, env, "add", "-A"); err != nil {
		return "", err
	}
	return git(r.Root, env, "write-tree")
}

func (r *Repo) Diff(from, to string) ([]File, error) {
	out, err := git(r.Root, nil, "diff", "--no-color", "--no-ext-diff", "--no-renames", "-U3", from, to)
	if err != nil {
		return nil, err
	}
	return parseDiff(out), nil
}

type Kind int

const (
	KTop Kind = iota
	KFile
	KMeta
	KHunk
	KCtx
	KAdd
	KDel
	KThread
)

type Line struct {
	Kind     Kind
	Text     string // raw diff line, prefix included for code lines
	Old, New int    // 0 when the line doesn't exist on that side
}

func (l Line) code() bool { return l.Kind == KCtx || l.Kind == KAdd || l.Kind == KDel }

// num is the line number shown to the Author: new side if it exists, else old side.
func (l Line) num() int {
	if l.New != 0 {
		return l.New
	}
	return l.Old
}

type File struct {
	Path  string
	Lines []Line
}

func parseDiff(s string) []File {
	var files []File
	var cur *File
	var o, n int
	inHunk := false
	for _, l := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			files = append(files, File{})
			cur = &files[len(files)-1]
			if i := strings.LastIndex(l, " b/"); i >= 0 {
				cur.Path = l[i+3:]
			}
			inHunk = false
		case cur == nil:
		case strings.HasPrefix(l, "@@"):
			f := strings.Fields(l)
			if len(f) >= 3 {
				fmt.Sscanf(f[1], "-%d", &o)
				fmt.Sscanf(f[2], "+%d", &n)
			}
			cur.Lines = append(cur.Lines, Line{Kind: KHunk, Text: l})
			inHunk = true
		case !inHunk:
			if p, ok := strings.CutPrefix(l, "+++ b/"); ok {
				cur.Path = p
			} else if strings.HasPrefix(l, "new file") || strings.HasPrefix(l, "deleted file") || strings.HasPrefix(l, "Binary") {
				cur.Lines = append(cur.Lines, Line{Kind: KMeta, Text: l})
			}
		case strings.HasPrefix(l, "+"):
			cur.Lines = append(cur.Lines, Line{Kind: KAdd, Text: l, New: n})
			n++
		case strings.HasPrefix(l, "-"):
			cur.Lines = append(cur.Lines, Line{Kind: KDel, Text: l, Old: o})
			o++
		case strings.HasPrefix(l, " "):
			cur.Lines = append(cur.Lines, Line{Kind: KCtx, Text: l, Old: o, New: n})
			o++
			n++
		}
	}
	return files
}

// sideText makes " x" and "+x" equal (both exist in the new file) but keeps "-x" distinct.
func sideText(raw string) string {
	if strings.HasPrefix(raw, "-") {
		return raw
	}
	if raw == "" {
		return "+"
	}
	return "+" + raw[1:]
}

// findAnchor returns the index of the last line of t's snippet in lines, choosing the
// match nearest to where the Thread was written, or -1 if the snippet is gone.
func findAnchor(lines []Line, t *Thread) int {
	if len(t.Lines) == 0 {
		return -1
	}
	best, bestDist := -1, 0
	for i := 0; i+len(t.Lines) <= len(lines); i++ {
		ok := true
		for k, want := range t.Lines {
			l := lines[i+k]
			if !l.code() || sideText(l.Text) != sideText(want) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		d := lines[i].num() - t.Line
		if d < 0 {
			d = -d
		}
		if best < 0 || d < bestDist {
			best, bestDist = i+len(t.Lines)-1, d
		}
	}
	return best
}
