package main

// The action button at the right of the status line: one button whose label follows the
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

var (
	stBtn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("4"))
	stBtnHov = stBtn.Background(lipgloss.Color("12"))
	stBtnOff = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).Background(lipgloss.Color("8"))
	stMenu   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("4"))
)

// quick picks the button's label and action; aNone comes with the reason it's disabled.
func quick(s GitState) (string, act, string) {
	open := s.PR != nil && s.PR.State == "OPEN"
	switch {
	case s.Branch == "HEAD":
		return "Commit", aNone, "detached HEAD: check out a branch first"
	case s.Dirty && !s.Remote:
		return "Commit", aCommit, ""
	case s.Dirty && open:
		return "Commit & push", aCommitPush, ""
	case s.Dirty:
		return "Commit, push & create PR", aCommitPushPR, ""
	case !s.Remote:
		return "Commit", aNone, "nothing to commit"
	case s.Behind > 0:
		return "Pull", aNone, "the branch is behind its upstream: pull first"
	case s.Ahead > 0 && (open || s.Default):
		return "Push", aPush, ""
	case s.Ahead > 0:
		return "Push & create PR", aPR, ""
	case open:
		return "Merge PR", aMerge, ""
	case s.PR != nil && s.PR.State == "MERGED":
		return "Merged", aNone, fmt.Sprintf("PR #%d is merged", s.PR.Number)
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
	open, branch := s.PR != nil && s.PR.State == "OPEN", s.Branch != "HEAD"
	return []item{
		{"Commit", aCommit, branch && s.Dirty},
		{"Push", aPush, branch && s.Remote && s.Ahead > 0 && s.Behind == 0},
		{"Create PR", aPR, branch && s.Remote && !open && !s.Default && s.Delta > 0},
		{"Merge PR", aMerge, open},
		{"View PR", aViewPR, s.PR != nil},
	}
}

// picker is a dropdown above the button: the git menu, the branch picker, the merge method.
type picker struct {
	title string
	items []item
	sel   int
	pick  func(int)
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
	s.PR = m.pr
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
	m.mode, m.menu = mMenu, &picker{title, items, sel, pick}
}

func (m *model) choose(i int) {
	if it := m.menu.items[i]; !it.on {
		m.msg = it.label + " isn't possible right now"
		return
	}
	m.mode = mNormal
	m.menu.pick(i)
}

func (m *model) menuKey(s string) {
	p := m.menu
	switch s {
	case "j", "down", "tab":
		p.sel = (p.sel + 1) % len(p.items)
	case "k", "up", "shift+tab":
		p.sel = (p.sel - 1 + len(p.items)) % len(p.items)
	case "enter":
		m.choose(p.sel)
	case "esc", "q", "C":
		m.mode = mNormal
	}
}

func (m *model) run(a act) {
	if m.busy != "" {
		m.msg = m.busy + " still running"
		return
	}
	if (a == aCommitPushPR || a == aPR || a == aMerge || a == aViewPR) && m.ghErr != "" {
		m.msg = m.ghErr
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
	then := func(branch string) {
		m.ask(mGit, "commit message", func(msg string) {
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
// is set, pushes and opens the PR. The title defaults to the commit message.
func (m *model) createPR(branch, msg string) {
	r := m.repoOn(branch)
	text := msg
	if text == "" {
		text, _ = git(r.Root, nil, "log", "-1", "--format=%B")
	}
	m.pickBranch("PR base", r.Branch, "", func(base string) {
		m.ask(mGit, "PR title, then the body", func(t string) {
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
		})
		m.input.SetValue(strings.TrimSpace(text))
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
			m.exec("merging…", func() (string, error) {
				return fmt.Sprintf("merged PR #%d into %s (%s)", pr.Number, base, methods[i]), r.MergePR(pr, base, methods[i])
			})
		})
	})
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
		def = strings.TrimPrefix(m.baseFlag, "origin/")
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
	m.pr, m.ghErr = p.pr, e
}

// button renders the split button: the action, and ▾ for the menu. It records where they are for the mouse.
func (m *model) button() string {
	label, a, _ := quick(m.gitState())
	if m.busy != "" {
		label, a = m.busy, aNone
	}
	main, caret := " ⎇ "+label+" ", "│ ▾ "
	m.caretX = m.w - lipgloss.Width(caret)
	m.btnX = m.caretX - lipgloss.Width(main)
	st, cst := stBtn, stBtn
	if a == aNone {
		st = stBtnOff
	}
	if m.my == m.h-1 && m.mx >= m.caretX {
		cst = stBtnHov
	} else if m.my == m.h-1 && m.mx >= m.btnX && a != aNone {
		st = stBtnHov
	}
	return st.Render(main) + cst.Render(caret)
}

// menuBox places the open menu at the bottom right of the diff pane, above the button:
// its left x, the screen row of its first shown item, the first shown item, and how many show.
func (m *model) menuBox() (x, y, off, n int) {
	p, vh := m.menu, m.viewH()
	n = min(len(p.items), 10, max(1, vh-3))
	off = max(0, p.sel-n+1)
	w := lipgloss.Width(p.title) + 2
	for _, it := range p.items {
		w = max(w, lipgloss.Width(it.label)+2)
	}
	w = min(max(w, 20), m.w/2) + 2 // + border
	return m.w - w, vh - n - 1, off, n
}

func (m *model) menuView() []string {
	p := m.menu
	x, _, off, n := m.menuBox()
	w := m.w - x - 2
	lines := []string{lipgloss.NewStyle().Bold(true).Width(w).Render(" " + p.title)}
	for i := off; i < off+n; i++ {
		st := lipgloss.NewStyle()
		if !p.items[i].on {
			st = stDone
		}
		if i == p.sel {
			st = st.Reverse(true)
		}
		lines = append(lines, st.Width(w).Render(ansi.Truncate(" "+p.items[i].label, w, "…")))
	}
	return strings.Split(stMenu.Render(strings.Join(lines, "\n")), "\n")
}

// overlayMenu draws the open menu over the bottom right of the diff pane lines.
func (m *model) overlayMenu(lines []string) {
	box := m.menuView()
	x, _, _, _ := m.menuBox()
	for j, l := range box {
		if y := len(lines) - len(box) + j; y >= 0 {
			s := ansi.Truncate(lines[y], x, "")
			lines[y] = s + "\x1b[0m" + strings.Repeat(" ", max(0, x-ansi.StringWidth(s))) + l
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
