package main

// The git action button at the right of the toolbar: one button whose label follows the
// branch's state (after T3 Code's git action control), plus a menu with each step.

import (
	"fmt"
	"os"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type act int

const (
	aNone act = iota
	aCommit
	aCommitPush
	aCommitPushPR
	aPush
	aPR // push & create PR
	aMerge
	aViewPR
)

// Set from the active theme by setTheme.
var stBtn, stBtnHov, stBtnOff, stMenu lipgloss.Style

// quick picks the button's label and action; aNone comes with the reason it's disabled.
func quick(s GitState) (string, act, string) {
	open := s.PR != nil && s.PR.State == "OPEN"
	switch {
	case s.Branch == "HEAD":
		return "Commit", aNone, "detached HEAD: check out a branch first"
	case s.Dirty && !s.Remote:
		return "Commit", aCommit, ""
	case s.Dirty && (open || s.NoPR != ""):
		return "Commit & push", aCommitPush, ""
	case s.Dirty:
		return "Commit, push & create PR", aCommitPushPR, ""
	case !s.Remote:
		return "Commit", aNone, "nothing to commit"
	case s.Behind > 0:
		return "Pull", aNone, "the branch is behind its upstream: pull first"
	case s.Ahead > 0 && (open || s.Default || s.NoPR != ""):
		return "Push", aPush, ""
	case s.Ahead > 0:
		return "Push & create PR", aPR, ""
	case open:
		return "Merge PR", aMerge, ""
	case s.PR != nil && s.PR.State == "MERGED":
		return "Merged", aNone, fmt.Sprintf("PR #%d is merged", s.PR.Number)
	case s.Delta > 0 && !s.Default && s.NoPR != "":
		return "Create PR", aNone, s.NoPR
	case s.Delta > 0 && !s.Default:
		return "Create PR", aPR, ""
	}
	return "Commit", aNone, "nothing to commit"
}

type item struct {
	label string
	act   act
	on    bool
}

func menuItems(s GitState) []item {
	open, branch, forge := s.PR != nil && s.PR.State == "OPEN", s.Branch != "HEAD", s.NoPR == ""
	return []item{
		{"Commit", aCommit, branch && s.Dirty},
		{"Push", aPush, branch && s.Remote && s.Ahead > 0 && s.Behind == 0},
		{"Create PR", aPR, forge && branch && s.Remote && !open && !s.Default && s.Delta > 0},
		{"Merge PR", aMerge, forge && open},
		{"View PR", aViewPR, forge && s.PR != nil},
	}
}

// picker is a dropdown below the button: the git menu, the branch picker, the merge method.
// sel and off are positions in shown, the items matching the search query.
type picker struct {
	title  string
	items  []item
	sel    int
	off    int // the first shown position; moves only to keep sel in view
	pick   func(int)
	search bool // "/" is typing a query
	query  string
}

// shown lists the indexes of the items whose label contains the query, ignoring case.
func (p *picker) shown() []int {
	var is []int
	for i, it := range p.items {
		if strings.Contains(strings.ToLower(it.label), strings.ToLower(p.query)) {
			is = append(is, i)
		}
	}
	return is
}

// head is the title line, with the query while searching.
func (p *picker) head() string {
	if p.search {
		return " " + p.title + "  /" + p.query
	}
	return " " + p.title
}

type gitDone struct {
	msg string
	err error
}

type prMsg struct {
	branch string
	pr     *PR
	err    error
}

func (m *model) gitState() GitState {
	s := m.d.git
	s.PR, s.NoPR = m.pr, m.noPR
	return s
}

func (m *model) primary() {
	_, a, hint := quick(m.gitState())
	if a == aNone && m.busy == "" {
		m.msg = hint
		return
	}
	m.run(a)
}

func (m *model) gitMenu() {
	items := menuItems(m.gitState())
	m.open("Git actions", items, 0, func(i int) { m.run(items[i].act) })
}

func (m *model) open(title string, items []item, sel int, pick func(int)) {
	m.mode, m.menu, m.help = mMenu, &picker{title: title, items: items, sel: sel, pick: pick}, false
}

// choose picks the item at shown position i.
func (m *model) choose(i int) {
	is := m.menu.shown()
	if i >= len(is) {
		return
	}
	if it := m.menu.items[is[i]]; !it.on {
		m.msg = it.label + " isn't possible right now"
		return
	}
	m.mode = mNormal
	m.menu.pick(is[i])
}

func (m *model) menuKey(s string) {
	p := m.menu
	is := p.shown()
	if p.search {
		switch r := []rune(p.query); {
		case s == "esc": // leaves the search, keeping the highlighted item
			if p.sel < len(is) {
				p.sel = is[p.sel]
			}
			p.search, p.query = false, ""
			return
		case s == "backspace" && len(r) > 0:
			p.query, p.sel = string(r[:len(r)-1]), 0
			return
		case len([]rune(s)) == 1:
			p.query, p.sel = p.query+s, 0
			return
		}
	}
	n := max(1, len(is))
	switch s {
	case "j", "down", "tab":
		p.sel = (p.sel + 1) % n
	case "k", "up", "shift+tab":
		p.sel = (p.sel - 1 + n) % n
	case "enter":
		m.choose(p.sel)
	case "/":
		p.search = true
	case "esc", "q", "C":
		m.mode = mNormal
	}
}

func (m *model) run(a act) {
	if m.busy != "" {
		m.msg = m.busy + " still running"
		return
	}
	r := *m.repo
	switch a {
	case aCommit, aCommitPush, aCommitPushPR:
		m.commit(a)
	case aPush:
		m.exec("pushing…", func() (string, error) { return "pushed " + r.Branch, r.Push() })
	case aPR:
		m.createPR("", "")
	case aMerge:
		m.merge()
	case aViewPR:
		url := m.pr.URL
		m.exec("opening PR…", func() (string, error) {
			_, err := gh(r.Root, "pr", "view", "--web", r.Branch)
			return url, err
		})
	}
}

// repoOn is the repo as it will be once a commit moved it onto branch.
func (m *model) repoOn(branch string) Repo {
	r := *m.repo
	if branch != "" {
		r.Branch = branch
	}
	return r
}

// commit asks for a new branch when on main/master, then for the message, and commits;
// with a PR to create, it first asks for the PR too, so Esc anywhere changes nothing.
func (m *model) commit(a act) {
	r := *m.repo
	then := func(branch string) {
		// Staged now so the draft describes exactly what gets committed; Esc leaves it staged.
		m.draft("commit message", commitPrompt, func() (string, error) {
			if _, err := git(r.Root, nil, "add", "-A"); err != nil {
				return "", err
			}
			return r.diffText("--cached")
		}, func(msg string) {
			if a == aCommitPushPR {
				m.createPR(branch, msg)
				return
			}
			r := m.repoOn(branch)
			m.exec("committing…", func() (string, error) {
				if err := r.Commit(branch, msg); err != nil {
					return "", err
				}
				if a == aCommit {
					return "committed", nil
				}
				return "committed and pushed", r.Push()
			})
		})
	}
	if !m.gitState().Default {
		then("")
		return
	}
	m.ask(mGit, "you're on "+m.repo.Branch+": new branch name for this commit", func(b string) {
		if _, err := git(m.repo.Root, nil, "check-ref-format", "--branch", b); err != nil {
			m.msg = "not a valid branch name: " + b
			return
		}
		then(b)
	})
}

// createPR asks for the base and the title (first line) and body, then commits when msg
// is set, pushes and opens the PR. The title and body come from the commit message, or
// Claude drafts them from the Changeset's commits.
func (m *model) createPR(branch, msg string) {
	r, from := m.repoOn(branch), m.d.base
	m.pickBranch("PR base", r.Branch, "", func(base string) {
		create := func(t string) {
			title, body, _ := strings.Cut(t, "\n")
			m.exec("creating PR…", func() (string, error) {
				if msg != "" {
					if err := r.Commit(branch, msg); err != nil {
						return "", err
					}
				}
				url, err := r.CreatePR(base, strings.TrimSpace(title), strings.TrimSpace(body))
				return "opened " + url, err
			})
		}
		if msg != "" {
			m.ask(mGit, "PR title, then the body", create)
			m.input.SetValue(msg)
			return
		}
		m.draft("PR title, then the body", prPrompt, func() (string, error) { return r.diffText(from, "HEAD") }, create)
	})
}

func (m *model) merge() {
	pr, r := *m.pr, *m.repo
	m.pickBranch(fmt.Sprintf("Merge PR #%d into", pr.Number), r.Branch, pr.Base, func(base string) {
		methods := []string{"merge", "squash", "rebase"}
		items := make([]item, len(methods))
		for i, s := range methods {
			items[i] = item{label: s, on: true}
		}
		m.open("Merge method", items, 1, func(i int) {
			m.busy = "merging…"
			m.cmd = func() tea.Msg {
				msg := fmt.Sprintf("merged PR #%d into %s (%s)", pr.Number, base, methods[i])
				if err := r.MergePR(pr, base, methods[i]); err != nil || !r.Linked() {
					return gitDone{msg, err}
				}
				lost, err := r.Unsaved()
				return merged{pr.Number, msg, lost, err}
			}
		})
	})
}

// merged is a merge from a linked worktree, which is then removed.
type merged struct {
	pr   int
	msg  string
	lost string // what removing the worktree would lose, "" if nothing
	err  error
}

// afterMerge removes the worktree, asking first when that would lose work.
func (m *model) afterMerge(d merged) {
	m.busy = ""
	switch {
	case d.err != nil:
		m.msg = d.msg + " · worktree kept: " + d.err.Error()
	case d.lost != "":
		m.msg = ""
		m.confirm(fmt.Sprintf("%s · removing the worktree loses %s: remove anyway? (y/n)", d.msg, d.lost), func(yes bool) {
			if yes {
				m.removeWorktree(d.pr, true)
			} else {
				m.msg = d.msg + " · worktree kept"
			}
		})
	default:
		m.removeWorktree(d.pr, false)
		return
	}
	m.cmd = m.fetchPR()
}

// removeWorktree deletes the branch and the worktree, then quits; on failure agentpr stays open with the error.
func (m *model) removeWorktree(pr int, force bool) {
	r := *m.repo
	m.busy, m.msg = "removing worktree…", fmt.Sprintf("merged #%d · removing worktree…", pr)
	m.cmd = func() tea.Msg {
		if err := r.Remove(herdrWorkspace(r.Root), force); err != nil {
			return gitDone{err: fmt.Errorf("merged #%d, but removing the worktree failed: %w", pr, err)}
		}
		return tea.QuitMsg{}
	}
}

func (m *model) confirm(q string, answer func(yes bool)) {
	m.mode, m.question, m.answer = mConfirm, q, answer
}

// pickBranch lists every branch but head; def "" preselects the Base's branch.
func (m *model) pickBranch(title, head, def string, f func(string)) {
	bs, err := m.repo.Branches()
	if err != nil {
		m.msg = err.Error()
		return
	}
	bs = slices.DeleteFunc(bs, func(b string) bool { return b == head })
	if len(bs) == 0 {
		m.msg = "no other branch to target"
		return
	}
	if def == "" {
		def = strings.TrimPrefix(m.d.baseRef, "origin/")
	}
	if def == "" && slices.Contains(bs, "master") && !slices.Contains(bs, "main") {
		def = "master"
	} else if def == "" {
		def = "main"
	}
	items, sel := make([]item, len(bs)), 0
	for i, b := range bs {
		items[i] = item{label: b, on: true}
		if b == def {
			sel = i
		}
	}
	m.open(title, items, sel, func(i int) { f(bs[i]) })
}

// pickBase switches the Base to another local or remote branch, and reloads right away.
func (m *model) pickBase() {
	refs, err := m.repo.Refs(m.repo.Branch)
	if err != nil || len(refs) == 0 {
		m.msg = "no other branch to compare against"
		return
	}
	sel := max(0, slices.Index(refs, m.d.baseRef))
	items := make([]item, len(refs))
	for i, ref := range refs {
		items[i] = item{label: ref, on: true}
	}
	m.open("Compare against", items, sel, func(i int) {
		m.baseFlag = refs[i]
		load := m.load()
		m.cmd = func() tea.Msg {
			d := load().(loaded)
			d.once = true
			return d
		}
	})
}

// exec runs git/gh off the UI goroutine; the outcome lands in the status line.
func (m *model) exec(busy string, f func() (string, error)) {
	m.busy = busy
	m.cmd = func() tea.Msg {
		msg, err := f()
		return gitDone{msg, err}
	}
}

func (m *model) done(d gitDone) tea.Cmd {
	m.busy, m.msg = "", d.msg
	if d.err != nil {
		m.msg = d.err.Error()
	}
	m.followBranch()
	return m.fetchPR()
}

// followBranch picks up the switch to a new branch after committing on main, and takes the Threads along.
func (m *model) followBranch() {
	b, err := git(m.repo.Root, nil, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || b == m.repo.Branch {
		return
	}
	r := *m.repo
	r.Branch = b
	if _, err := os.Stat(r.StatePath()); os.IsNotExist(err) {
		os.Rename(m.repo.StatePath(), r.StatePath())
	}
	m.repo, m.pr, m.sig = &r, nil, ""
}

func (m *model) fetchPR() tea.Cmd {
	r := *m.repo
	if r.Branch == "HEAD" {
		return nil
	}
	return func() tea.Msg {
		pr, err := r.PR()
		return prMsg{r.Branch, pr, err}
	}
}

func (m *model) gotPR(p prMsg) {
	if p.branch != m.repo.Branch {
		return
	}
	e := ""
	if p.err != nil {
		e = p.err.Error()
	}
	if e != m.ghErr && e != "" {
		m.msg = e
	}
	u, _ := p.err.(noPR) // a network failure leaves PRs available
	m.pr, m.ghErr, m.noPR = p.pr, e, string(u)
}

// menuBox places the open menu at the top right of the diff pane, below the button:
// its left x, the screen row of its first shown item, the first shown item, and how many show.
func (m *model) menuBox() (x, y, off, n int) {
	p, vh := m.menu, m.viewH()
	k := len(p.shown())
	n = min(k, 10, max(1, vh-3))
	p.off = max(0, min(p.off, p.sel, k-n), p.sel-n+1)
	w := lipgloss.Width(p.head()) + 1
	for _, it := range p.items {
		w = max(w, lipgloss.Width(it.label)+2)
	}
	w = min(max(w, 20), m.w/2) + 2 // + border
	return m.w - w, 3, p.off, n    // below the toolbar, the top border and the title
}

func (m *model) menuView() []string {
	p := m.menu
	x, _, off, n := m.menuBox()
	w := m.w - x - 2
	lines := []string{stCtx.Bold(true).Width(w).Render(ansi.Truncate(p.head(), w, "…"))}
	is := p.shown()
	for i := off; i < off+n; i++ {
		st := stCtx
		if !p.items[is[i]].on {
			st = stDone
		}
		if i == p.sel {
			st = st.Reverse(true)
		}
		lines = append(lines, st.Width(w).Render(ansi.Truncate(" "+p.items[is[i]].label, w, "…")))
	}
	return strings.Split(stMenu.Render(strings.Join(lines, "\n")), "\n")
}

// overlay draws box over the top right of the diff pane lines.
func overlay(lines, box []string) {
	if len(box) > 0 && len(lines) > 0 {
		overlayAt(lines, box, ansi.StringWidth(lines[0])-ansi.StringWidth(box[0]), 0)
	}
}

// overlayAt draws box with its top left corner at column x, line y of lines.
func overlayAt(lines, box []string, x, y int) {
	for i, l := range box {
		if j := y + i; j >= 0 && j < len(lines) {
			s := ansi.Truncate(lines[j], x, "")
			tail := ansi.TruncateLeft(lines[j], x+ansi.StringWidth(l), "")
			lines[j] = s + "\x1b[0m" + strings.Repeat(" ", max(0, x-ansi.StringWidth(s))) + l + tail
		}
	}
}

// menuMouse: hovering moves the selection, clicking an item picks it, clicking elsewhere closes.
func (m *model) menuMouse(e tea.MouseMsg) {
	x, y, off, n := m.menuBox()
	i := off + e.Y - y
	in := e.X >= x && e.Y >= y && e.Y < y+n
	switch {
	case e.Button == tea.MouseButtonWheelUp:
		m.menuKey("k")
	case e.Button == tea.MouseButtonWheelDown:
		m.menuKey("j")
	case e.Action == tea.MouseActionMotion && in:
		m.menu.sel = i
	case e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft && in:
		m.menu.sel = i
		m.choose(i)
	case e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft:
		m.mode = mNormal
	}
}
