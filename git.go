package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
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

func openRepo(dir string) (*Repo, error) {
	root, err := git(dir, nil, "rev-parse", "--show-toplevel")
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

// Base returns the Base commit and the ref it came from: the merge-base of HEAD with ref
// (--base or the picker), or with the default ref when ref is "".
func (r *Repo) Base(ref string) (string, string, error) {
	if _, err := git(r.Root, nil, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		if ref == "" {
			return emptyTree, "empty tree", nil // no commits yet
		}
		c, err := git(r.Root, nil, "rev-parse", "--verify", ref+"^{commit}")
		return c, ref, err
	}
	if ref == "" {
		ref = r.defaultBase()
	}
	if _, err := git(r.Root, nil, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return "", "", err
	}
	mb, err := git(r.Root, nil, "merge-base", "HEAD", ref)
	if err != nil {
		return "", "", fmt.Errorf("%s shares no history with %s", ref, r.Branch)
	}
	return mb, ref, nil
}

// defaultBase: on main/master its upstream, so unpushed commits show; on a feature branch
// main/master, or origin's when that is ahead; else HEAD.
func (r *Repo) defaultBase() string {
	if r.Branch == "main" || r.Branch == "master" {
		if u, err := git(r.Root, nil, "rev-parse", "--abbrev-ref", "--verify", "-q", "@{u}"); err == nil {
			return u
		}
		return "HEAD"
	}
	for _, b := range []string{"main", "master"} {
		local, lerr := git(r.Root, nil, "rev-parse", "--verify", "-q", b)
		remote, rerr := git(r.Root, nil, "rev-parse", "--verify", "-q", "origin/"+b)
		switch {
		case lerr == nil && rerr == nil && local != remote:
			if _, err := git(r.Root, nil, "merge-base", "--is-ancestor", b, "origin/"+b); err == nil {
				return "origin/" + b
			}
			return b
		case lerr == nil:
			return b
		case rerr == nil:
			return "origin/" + b
		}
	}
	return "HEAD"
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

// GitState is what the action button needs to know about the branch; PR comes from gh separately.
type GitState struct {
	Branch        string
	Default       bool // on main/master
	Dirty         bool // the working tree differs from HEAD
	Remote        bool
	Ahead, Behind int // commits on no remote yet / upstream commits missing here
	Delta         int // commits since the Base
	PR            *PR
	NoPR          string // why PRs can't work here (gh missing, logged out, no GitHub remote); "" if they can
}

func (r *Repo) State(base, tree string) GitState {
	s := GitState{Branch: r.Branch, Default: r.Branch == "main" || r.Branch == "master"}
	head, err := git(r.Root, nil, "rev-parse", "-q", "--verify", "HEAD^{tree}")
	if err != nil {
		head = emptyTree
	}
	s.Dirty = tree != head
	remotes, _ := git(r.Root, nil, "remote")
	s.Remote = remotes != ""
	count := func(args ...string) int {
		out, _ := git(r.Root, nil, append([]string{"rev-list", "--count"}, args...)...)
		n, _ := strconv.Atoi(out)
		return n
	}
	s.Ahead = count("HEAD", "--not", "--remotes")
	s.Behind = count("HEAD..@{u}")
	s.Delta = count(base + "..HEAD")
	return s
}

// Branches lists local and remote branches by name, without the remote prefix.
func (r *Repo) Branches() ([]string, error) {
	out, err := git(r.Root, nil, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	return parseBranches(out), err
}

// Refs lists local and remote branches as git names them (main, origin/main), but not head.
func (r *Repo) Refs(head string) ([]string, error) {
	out, err := git(r.Root, nil, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	var refs []string
	for _, ref := range strings.Split(out, "\n") {
		if ref = strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/"); ref != "" && ref != head && !strings.HasSuffix(ref, "/HEAD") {
			refs = append(refs, ref)
		}
	}
	return refs, err
}

func parseBranches(out string) []string {
	var bs []string
	for _, ref := range strings.Split(out, "\n") {
		b, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok {
			_, b, _ = strings.Cut(strings.TrimPrefix(ref, "refs/remotes/"), "/")
		}
		if b != "" && b != "HEAD" && !slices.Contains(bs, b) {
			bs = append(bs, b)
		}
	}
	return bs
}

// Commit stages everything the Changeset shows (tracked + untracked) and commits it,
// switching to a new branch first when branch is set.
func (r *Repo) Commit(branch, msg string) error {
	if branch != "" {
		if _, err := git(r.Root, nil, "switch", "-c", branch); err != nil {
			return err
		}
	}
	if _, err := git(r.Root, nil, "add", "-A"); err != nil {
		return err
	}
	_, err := git(r.Root, nil, "commit", "-q", "-m", msg)
	return err
}

// Push publishes the branch; a credential prompt fails instead of hanging the TUI.
func (r *Repo) Push() error {
	_, err := git(r.Root, []string{"GIT_TERMINAL_PROMPT=0"}, "push", "-q", "-u", "origin", "HEAD")
	return err
}

// noPR is a gh error meaning PRs can't work in this repo at all, unlike a network failure.
type noPR string

func (e noPR) Error() string { return string(e) }

// gh runs the GitHub CLI, turning "not installed", "not logged in" and "not on GitHub" into messages a person can act on.
func gh(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")
	out, err := cmd.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return "", noPR("gh not found: PRs need the GitHub CLI (cli.github.com)")
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return "", ghFail(args, string(ee.Stderr))
	}
	return strings.TrimSpace(string(out)), err
}

func ghFail(args []string, stderr string) error {
	msg := strings.TrimSpace(stderr)
	switch {
	case strings.Contains(msg, "known GitHub host"): // its hint also says "gh auth login"
		return noPR("origin isn't a GitHub remote: PRs unavailable")
	case strings.Contains(msg, "gh auth login"):
		return noPR("not logged in to GitHub, run: gh auth login")
	}
	return fmt.Errorf("gh %s: %s", strings.Join(args[:min(2, len(args))], " "), msg)
}

type PR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"` // OPEN, CLOSED or MERGED
	Base   string `json:"baseRefName"`
}

// PR returns the branch's latest pull request, or nil if it has none.
func (r *Repo) PR() (*PR, error) {
	out, err := gh(r.Root, "pr", "view", r.Branch, "--json", "number,url,state,baseRefName")
	if err != nil {
		if strings.Contains(err.Error(), "no pull requests found") {
			return nil, nil
		}
		return nil, err
	}
	pr := &PR{}
	return pr, json.Unmarshal([]byte(out), pr)
}

// CreatePR pushes the branch and opens a pull request into base; it returns the PR's URL.
func (r *Repo) CreatePR(base, title, body string) (string, error) {
	if err := r.Push(); err != nil {
		return "", err
	}
	out, err := gh(r.Root, "pr", "create", "--base", base, "--head", r.Branch, "--title", title, "--body", body)
	return out[strings.LastIndex(out, "\n")+1:], err
}

// MergePR merges pr with method (merge, squash or rebase), retargeting it to base first if that changed.
func (r *Repo) MergePR(pr PR, base, method string) error {
	n := strconv.Itoa(pr.Number)
	if base != pr.Base {
		if _, err := gh(r.Root, "pr", "edit", n, "--base", base); err != nil {
			return err
		}
	}
	_, err := gh(r.Root, "pr", "merge", n, "--"+method)
	return err
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
	KGap // blank spacer row before a file header; the cursor never stops on it
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
	Path   string
	Status string // new, deleted, modified or "mode 100644 → 100755", plus " · binary"
	Lines  []Line
}

// stats counts the File's added and removed lines.
func (f File) stats() (add, del int) {
	for _, l := range f.Lines {
		if l.Kind == KAdd {
			add++
		} else if l.Kind == KDel {
			del++
		}
	}
	return add, del
}

func parseDiff(s string) []File {
	var files []File
	var cur *File
	var o, n int
	inHunk := false
	for _, l := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			files = append(files, File{Status: "modified"})
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
			// Git's meta lines fold into Status; index lines say nothing a reviewer needs.
			if p, ok := strings.CutPrefix(l, "+++ b/"); ok {
				cur.Path = p
			} else if strings.HasPrefix(l, "new file") {
				cur.Status = "new"
			} else if strings.HasPrefix(l, "deleted file") {
				cur.Status = "deleted"
			} else if m, ok := strings.CutPrefix(l, "old mode "); ok {
				cur.Status = "mode " + m
			} else if m, ok := strings.CutPrefix(l, "new mode "); ok {
				cur.Status += " → " + m
			} else if strings.HasPrefix(l, "Binary") {
				cur.Status += " · binary"
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

// Linked is whether r is a linked worktree (git worktree add), not the main checkout.
func (r *Repo) Linked() bool {
	d, err := git(r.Root, nil, "rev-parse", "--path-format=absolute", "--git-dir")
	return err == nil && d != r.GitDir
}

// Unsaved says what removing the worktree would lose: uncommitted changes, untracked files
// and commits on no remote; "" when nothing.
func (r *Repo) Unsaved() (string, error) {
	st, err := git(r.Root, nil, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	changed, untracked := 0, 0
	for _, l := range strings.Split(st, "\n") {
		if strings.HasPrefix(l, "??") {
			untracked++
		} else if l != "" {
			changed++
		}
	}
	out, err := git(r.Root, nil, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		return "", err
	}
	commits, _ := strconv.Atoi(out)
	var lost []string
	for _, c := range []struct {
		n         int
		one, many string
	}{{changed, "uncommitted change", "uncommitted changes"}, {untracked, "untracked file", "untracked files"}, {commits, "commit on no remote", "commits on no remote"}} {
		if c.n == 1 {
			lost = append(lost, "1 "+c.one)
		} else if c.n > 1 {
			lost = append(lost, fmt.Sprintf("%d %s", c.n, c.many))
		}
	}
	return strings.Join(lost, ", "), nil
}

type step struct {
	dir string
	cmd []string // git or herdr, and its arguments
}

// removeSteps deletes the merged branch everywhere, from the main checkout main: the remote
// branch, the local one (detached first: git won't delete a checked-out branch), then the
// worktree, through herdr when ws is its herdr workspace, which also closes the workspace
// and with it maybe agentpr's own pane, so it comes last.
func removeSteps(r Repo, main, ws string, force bool) []step {
	steps := []step{
		{r.Root, []string{"git", "push", "-q", "origin", "--delete", r.Branch}},
		{r.Root, []string{"git", "switch", "-q", "--detach"}},
		{main, []string{"git", "branch", "-q", "-D", r.Branch}},
	}
	rm := []string{"git", "worktree", "remove", r.Root}
	if ws != "" {
		rm = []string{"herdr", "worktree", "remove", "--workspace", ws}
	}
	if force {
		rm = append(rm, "--force")
	}
	return append(steps, step{main, rm})
}

// Remove runs removeSteps; force also removes uncommitted and untracked files.
func (r *Repo) Remove(ws string, force bool) error {
	out, err := git(r.Root, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	main := strings.TrimPrefix(strings.SplitN(out, "\n", 2)[0], "worktree ")
	for _, s := range removeSteps(*r, main, ws, force) {
		if s.cmd[0] == "herdr" {
			err = herdrJSON(nil, s.cmd[1:]...)
		} else if _, err = git(s.dir, []string{"GIT_TERMINAL_PROMPT=0"}, s.cmd[1:]...); err != nil && strings.Contains(err.Error(), "remote ref does not exist") {
			err = nil // GitHub deleted it on merge
		}
		if err != nil {
			return err
		}
	}
	return nil
}
