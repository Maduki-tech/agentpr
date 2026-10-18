package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type mode int

const (
	mNormal mode = iota
	mVisual
	mInput
	mSearch
	mSubmit
	mPick
	mGit     // typing a branch name, commit message or PR text
	mMenu    // the git menu or one of its pickers
	mConfirm // a y/n question
)

var modeNames = []string{"NORMAL", "VISUAL", "COMMENT", "SEARCH", "SUBMIT", "SEND TO", "GIT", "GIT", "CONFIRM"}

// Set from the active theme by setTheme.
var stTop, stFile, stHunk, stAdd, stDel, stCtx, stThread, stDone, stSel, stHover, stStatus, stCurFile lipgloss.Style

// Row is one screen line of the diff pane: a diff line or a line of an inline Thread.
type Row struct {
	Kind   Kind
	Path   string
	Line   Line
	Thread *Thread
	First  bool   // first row of its Thread
	Text   string // rendered text for top and thread rows
}

type loaded struct {
	baseFlag string // the ref asked for, so a load started before a Base switch is dropped
	once     bool   // a reload after a Base switch, outside the polling loop
	base     string
	baseRef  string // what the Base came from: main, origin/main, HEAD…
	tree     string
	files    []File
	state    *State
	raw      []byte
	agents   []Agent
	git      GitState
	err      error
}

type tickMsg struct{}

type model struct {
	repo     *Repo
	baseFlag string
	d        loaded
	sig      string
	focus    string // only this file is shown in the diff pane
	rows     []Row
	cur, top int
	anchor   int // visual-mode start row
	mode     mode
	input    textarea.Model
	onInput  func(string)
	search   string
	prefix   string // pending "g", "]" or "["
	w, h     int
	msg      string
	prompt   string // Review waiting for an agent pick
	mx, my   int    // mouse position, for hover
	press    int    // row the left button went down on
	armed    bool   // press landed on the current row/selection; release there comments
	listOff  int    // first file shown in the file list
	menu     *picker
	busy     string  // git/gh step running, shown on the action button
	cmd      tea.Cmd // started by a key or click handler, picked up by Update
	pr       *PR
	ghErr    string
	noPR     string // GitState.NoPR, from the last gh answer
	bar      []tool // the toolbar's buttons as last drawn, for the mouse
	help     bool   // the key list is shown
	gen      int    // counts input boxes, so a late draft lands only in the box it was made for
	redraft  func() // asks Claude again for the open input's text (ctrl+r)
	question string // the y/n question in mConfirm
	answer   func(yes bool)
	ticks    int
	hl       map[string]*highlight
}

func newModel(r *Repo, base string) *model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = "│ "
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j"))
	ta.SetHeight(3)
	return &model{repo: r, baseFlag: base, input: ta, mx: -1, my: -1, hl: map[string]*highlight{}}
}

func (m *model) Init() tea.Cmd { return m.load() }

// load runs off the UI goroutine, so it takes the repo up front: a commit on main swaps it for a new branch.
func (m *model) load() tea.Cmd {
	repo, baseFlag := m.repo, m.baseFlag
	return func() tea.Msg {
		d := loaded{baseFlag: baseFlag}
		if d.base, d.baseRef, d.err = repo.Base(baseFlag); d.err != nil {
			return d
		}
		if d.tree, d.err = repo.Snapshot(); d.err != nil {
			return d
		}
		if d.files, d.err = repo.Diff(d.base, d.tree); d.err != nil {
			return d
		}
		if d.state, d.raw, d.err = loadState(repo.StatePath()); d.err != nil {
			return d
		}
		d.git = repo.State(d.base, d.tree)
		d.agents, _ = agents(repo.Root)
		return d
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.input.SetWidth(msg.Width - 2)
		if m.d.state != nil {
			m.rebuild()
		}
	case loaded:
		var next tea.Cmd
		if !msg.once {
			// ponytail: polls git every 1.5s; switch to inotify if big repos make this slow.
			next = tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
			// ponytail: gh asks GitHub for the PR every ~30s and after each git action; a webhook would be instant.
			if m.ticks++; m.ticks%20 == 1 {
				next = tea.Batch(next, m.fetchPR())
			}
		}
		if msg.baseFlag != m.baseFlag {
			return m, next
		}
		if msg.err != nil {
			m.msg = msg.err.Error()
			return m, next
		}
		if sig := msg.baseRef + msg.base + msg.tree + string(msg.raw); sig != m.sig {
			m.d, m.sig = msg, sig
			m.rebuild()
		}
		m.d.agents, m.d.git = msg.agents, msg.git
		return m, next
	case tickMsg:
		return m, m.load()
	case gitDone:
		return m, m.done(msg)
	case merged:
		m.afterMerge(msg)
		cmd := m.cmd
		m.cmd = nil
		return m, cmd
	case prMsg:
		m.gotPR(msg)
	case drafted:
		m.gotDraft(msg)
	case tea.KeyMsg:
		cmd := m.key(msg)
		cmd, m.cmd = tea.Batch(cmd, m.cmd), nil
		return m, cmd
	case tea.MouseMsg:
		m.mouse(msg)
		cmd := m.cmd
		m.cmd = nil
		return m, cmd
	}
	return m, nil
}

func (m *model) typing() bool { return m.mode == mInput || m.mode == mSearch || m.mode == mGit }

func (m *model) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if m.d.state == nil {
		if s == "q" || s == "ctrl+c" {
			return tea.Quit
		}
		return nil
	}
	m.msg = ""
	// Bubble Tea batches fast keystrokes ("jjj", key repeat) into one event; outside the input box, replay them one by one.
	if k.Type == tea.KeyRunes && len(k.Runes) > 1 && !m.typing() {
		var cmds []tea.Cmd
		for _, r := range k.Runes {
			cmds = append(cmds, m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}))
		}
		return tea.Batch(cmds...)
	}
	switch m.mode {
	case mInput, mSearch, mGit:
		switch s {
		case "esc":
			m.mode = mNormal
			m.input.Blur()
		case "ctrl+r":
			if m.redraft != nil {
				m.redraft()
			}
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			m.mode = mNormal
			m.input.Blur()
			if text != "" {
				m.onInput(text)
			}
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(k)
			return cmd
		}
		return nil
	case mSubmit:
		m.mode = mNormal
		switch s {
		case "r":
			m.requestChanges()
		case "a":
			m.approve()
		}
		return nil
	case mMenu:
		m.menuKey(s)
		return nil
	case mConfirm:
		m.mode = mNormal
		m.answer(s == "y")
		return nil
	case mPick:
		m.mode = mNormal
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(m.d.agents) {
			m.deliver(m.d.agents[n-1])
		} else {
			m.msg = "review not sent"
		}
		return nil
	}

	if m.prefix != "" {
		p := m.prefix + s
		m.prefix = ""
		switch p {
		case "gg":
			m.cur = 0
		case "]c":
			m.jump(1, func(r Row) bool { return r.Kind == KHunk })
		case "[c":
			m.jump(-1, func(r Row) bool { return r.Kind == KHunk })
		case "]f":
			m.jump(1, func(r Row) bool { return r.Kind == KFile })
		case "[f":
			m.jump(-1, func(r Row) bool { return r.Kind == KFile })
		case "]t":
			m.jump(1, func(r Row) bool { return r.First })
		case "[t":
			m.jump(-1, func(r Row) bool { return r.First })
		}
		return nil
	}
	switch s {
	case "q", "ctrl+c":
		return tea.Quit
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "ctrl+d":
		m.move(m.viewH() / 2)
	case "ctrl+u":
		m.move(-m.viewH() / 2)
	case "G":
		m.move(len(m.rows))
	case "g", "]", "[":
		m.prefix = s
	case "v", "V":
		if m.mode == mVisual {
			m.mode = mNormal
		} else if m.rows[m.cur].Line.code() {
			m.mode, m.anchor = mVisual, m.cur
		}
	case "esc":
		if m.mode == mNormal && m.focus != "" && !m.help {
			m.focus = ""
			m.rebuild()
		}
		m.mode, m.help = mNormal, false
	case "enter":
		m.enter()
	case "R":
		if t := m.rows[m.cur].Thread; t != nil {
			m.mutate(func(s *State) error {
				if t := s.Thread(t.ID); t != nil {
					t.Resolved = !t.Resolved
				}
				return nil
			})
		}
	case "S":
		m.mode = mSubmit
	case "c":
		m.primary()
	case "C":
		m.gitMenu()
	case "T":
		m.msg = "theme: " + nextTheme()
	case "?":
		m.help = !m.help
	case "b":
		m.pickBase()
	case "/":
		m.ask(mSearch, "search", func(q string) { m.search = q; m.find(1) })
	case "n":
		m.find(1)
	case "N":
		m.find(-1)
	}
	return nil
}

// mouse: wheel scrolls, hover highlights, click selects a row or file, drag selects lines,
// and clicking the current row (or inside the selection) comments on it, like Enter.
func (m *model) mouse(e tea.MouseMsg) {
	m.mx, m.my = e.X, e.Y
	if m.d.state == nil {
		return
	}
	if m.mode == mMenu {
		m.menuMouse(e)
		return
	}
	switch e.Button {
	case tea.MouseButtonWheelUp:
		m.move(-3)
		return
	case tea.MouseButtonWheelDown:
		m.move(3)
		return
	}
	if m.mode != mNormal && m.mode != mVisual {
		return
	}
	if e.Y == 0 {
		if e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft {
			m.toolClick(e.X)
		}
		return
	}
	y := e.Y - 1 // row in the panes, below the toolbar
	if y >= m.viewH() {
		return
	}
	if e.X < m.listW() {
		if files := m.files(); e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft && m.listOff+y < len(files) {
			p := files[m.listOff+y].Path
			if m.focus == p {
				p = "" // clicking the focused file again shows all files
			}
			m.mode, m.focus = mNormal, p
			m.rebuild()
			for i, r := range m.rows {
				if r.Kind == KFile && r.Path == files[m.listOff+y].Path {
					m.cur = i
				}
			}
		}
		return
	}
	i := m.top + y
	if i >= len(m.rows) {
		return
	}
	switch {
	case e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft:
		m.msg = ""
		lo, hi := m.cur, m.cur
		if m.mode == mVisual {
			lo, hi = min(m.anchor, m.cur), max(m.anchor, m.cur)
		}
		m.press, m.armed = i, i >= lo && i <= hi
		if !m.armed {
			m.mode, m.cur = mNormal, i
		}
	case e.Action == tea.MouseActionMotion && e.Button == tea.MouseButtonLeft:
		if i != m.press && m.press < len(m.rows) && m.rows[m.press].Line.code() {
			m.armed = false
			m.mode, m.anchor, m.cur = mVisual, m.press, i
		}
	case e.Action == tea.MouseActionRelease:
		if m.armed && i == m.press {
			m.enter()
		}
		m.armed = false
	}
}

func (m *model) move(d int) { m.cur = max(0, min(len(m.rows)-1, m.cur+d)) }

func (m *model) jump(dir int, pred func(Row) bool) {
	if i := m.next(dir, pred); i >= 0 {
		m.cur = i
	}
}

// next is the nearest row after (dir 1) or before (dir -1) the cursor that matches pred, or -1.
func (m *model) next(dir int, pred func(Row) bool) int {
	for i := m.cur + dir; i >= 0 && i < len(m.rows); i += dir {
		if pred(m.rows[i]) {
			return i
		}
	}
	return -1
}

func (m *model) find(dir int) {
	if m.search == "" {
		return
	}
	q, n := strings.ToLower(m.search), len(m.rows)
	for k := 1; k <= n; k++ {
		i := ((m.cur+dir*k)%n + n) % n
		r := m.rows[i]
		if strings.Contains(strings.ToLower(r.Line.Text+r.Text+r.Path), q) {
			m.cur = i
			return
		}
	}
	m.msg = "not found: " + m.search
}

func (m *model) ask(md mode, placeholder string, f func(string)) {
	m.mode, m.onInput, m.redraft = md, f, nil
	m.gen++
	m.input.Reset()
	m.input.Placeholder = placeholder
	// Themed here, not in setTheme: the theme can't change while the box is open.
	m.input.FocusedStyle = textarea.Style{Base: stCtx, CursorLine: stHover, Text: stCtx, EndOfBuffer: stDone, Placeholder: stDone, Prompt: stHunk}
	m.input.Cursor.Style = stCtx.Reverse(true)
	m.input.Focus()
}

// enter means "say something here": reply on a Thread, else start one on the selection, line, file or Changeset.
func (m *model) enter() {
	r := m.rows[m.cur]
	switch {
	case m.mode == mVisual || (r.Thread == nil && r.Line.code()):
		lo, hi := m.cur, m.cur
		if m.mode == mVisual {
			lo, hi = min(m.anchor, m.cur), max(m.anchor, m.cur)
		}
		m.mode = mNormal
		t := &Thread{Path: r.Path}
		for i := lo; i <= hi; i++ {
			if x := m.rows[i]; x.Path == r.Path && x.Line.code() {
				if len(t.Lines) == 0 {
					t.Line = x.Line.num()
				}
				t.Lines = append(t.Lines, x.Line.Text)
			}
		}
		if len(t.Lines) > 0 {
			m.newThread(t)
		}
	case r.Thread != nil:
		id := r.Thread.ID
		m.ask(mInput, "reply to "+id, func(body string) {
			m.mutate(func(s *State) error {
				t := s.Thread(id)
				if t == nil {
					return fmt.Errorf("thread %s is gone", id)
				}
				t.Comments = append(t.Comments, Comment{Author: Reviewer, Body: body, Pending: true, At: time.Now()})
				t.Resolved = false
				return nil
			})
		})
	case r.Kind == KFile:
		m.newThread(&Thread{Path: r.Path})
	case r.Kind == KTop:
		m.newThread(&Thread{})
	}
}

func (m *model) newThread(t *Thread) {
	m.ask(mInput, "comment on "+t.Where(), func(body string) {
		m.mutate(func(s *State) error {
			s.NextID++
			t.ID = fmt.Sprintf("t%d", s.NextID)
			t.Comments = []Comment{{Author: Reviewer, Body: body, Pending: true, At: time.Now()}}
			s.Threads = append(s.Threads, t)
			return nil
		})
	})
}

func (m *model) mutate(f func(*State) error) bool {
	s, err := updateState(m.repo.StatePath(), f)
	if err != nil {
		m.msg = err.Error()
		return false
	}
	m.d.state = s
	m.rebuild()
	return true
}

func (m *model) requestChanges() {
	var ts []*Thread
	for _, t := range m.d.state.Threads {
		if t.Pending() {
			ts = append(ts, t)
		}
	}
	if len(ts) == 0 {
		m.msg = "nothing pending to send"
		return
	}
	m.prompt = buildPrompt(ts)
	switch len(m.d.agents) {
	case 0:
		m.deliver(Agent{})
	case 1:
		m.deliver(m.d.agents[0])
	default:
		m.mode = mPick
	}
}

// deliver sends the Review to a Claude pane, falling back to the clipboard, then to a file.
func (m *model) deliver(a Agent) {
	err := errors.New("no Claude pane in herdr for this repo")
	if a.Pane != "" {
		err = sendPrompt(a.Pane, m.prompt)
	}
	how := "review sent to Claude"
	if a.Status == "working" {
		how += " (busy; queued for after this turn)"
	}
	if err != nil {
		how = err.Error() + "; review copied to clipboard"
		if copyClipboard(m.prompt) != nil {
			p := filepath.Join(filepath.Dir(m.repo.StatePath()), "last-review.md")
			if werr := os.WriteFile(p, []byte(m.prompt), 0o644); werr != nil {
				m.msg = "could not deliver review: " + werr.Error()
				return
			}
			how = err.Error() + "; review written to " + p
		}
	}
	if m.mutate(func(s *State) error {
		for _, t := range s.Threads {
			for i := range t.Comments {
				t.Comments[i].Pending = false
			}
		}
		return nil
	}) {
		m.msg = how
	}
}

func (m *model) approve() {
	if m.mutate(func(s *State) error {
		s.Archived = append(s.Archived, s.Threads...)
		s.Threads = nil
		return nil
	}) {
		m.msg = "approved; threads archived"
	}
}

func (m *model) files() []File { return m.d.files }

func rowKey(r Row) string {
	switch {
	case r.Thread != nil:
		return "t:" + r.Thread.ID
	case r.Line.code():
		return "l:" + r.Path + ":" + r.Line.Text
	}
	return fmt.Sprintf("%d:%s", r.Kind, r.Path)
}

// rebuild re-renders rows and keeps the cursor on the same row (nearest match), or nearby.
func (m *model) rebuild() {
	old, oldKey := m.cur, ""
	if m.cur < len(m.rows) {
		oldKey = rowKey(m.rows[m.cur])
	}
	m.build()
	best := -1
	for i, r := range m.rows {
		if rowKey(r) == oldKey && (best < 0 || abs(i-old) < abs(best-old)) {
			best = i
		}
	}
	if best < 0 {
		best = old
	}
	m.cur = max(0, min(len(m.rows)-1, best))
	if m.mode == mVisual && (m.anchor >= len(m.rows) || m.rows[m.anchor].Path != m.rows[m.cur].Path) {
		m.mode = mNormal
	}
}

func (m *model) build() {
	st := m.d.state
	files := m.files()
	shown, lines := map[string]bool{}, map[string][]Line{}
	for _, f := range files {
		shown[f.Path], lines[f.Path] = true, f.Lines
	}
	for _, t := range st.Threads {
		t.Outdated = len(t.Lines) > 0 && findAnchor(lines[t.Path], t) < 0
	}

	label := fmt.Sprintf("Changeset vs %s (%s)", m.d.baseRef, m.d.base[:min(8, len(m.d.base))])
	rows := []Row{{Kind: KTop, Text: fmt.Sprintf("%s · %d files", label, len(files))}}
	if shown[m.focus] {
		files = slices.DeleteFunc(slices.Clone(files), func(f File) bool { return f.Path != m.focus })
		rows[0].Text += " · showing " + m.focus + " (esc: all)"
	} else {
		m.focus = ""
		for _, t := range st.Threads {
			if t.Path == "" || !shown[t.Path] {
				rows = m.threadRows(rows, t)
			}
		}
	}
	for _, f := range files {
		if h := m.hl[f.Path]; h == nil || !slices.Equal(h.lines, f.Lines) {
			m.hl[f.Path] = &highlight{lines: f.Lines}
		}
		rows = append(rows, Row{Kind: KFile, Path: f.Path})
		at := map[int][]*Thread{}
		for _, t := range st.Threads {
			if t.Path != f.Path {
				continue
			}
			if i := findAnchor(f.Lines, t); i >= 0 {
				at[i] = append(at[i], t)
			} else {
				rows = m.threadRows(rows, t) // file-level, outdated, or anchored outside this view
			}
		}
		for i, l := range f.Lines {
			rows = append(rows, Row{Kind: l.Kind, Path: f.Path, Line: l})
			for _, t := range at[i] {
				rows = m.threadRows(rows, t)
			}
		}
	}
	m.rows = rows
}

func (m *model) threadRows(rows []Row, t *Thread) []Row {
	add := func(s string, first bool) {
		rows = append(rows, Row{Kind: KThread, Path: t.Path, Thread: t, Text: s, First: first})
	}
	status := "open"
	if t.Resolved {
		status = "resolved"
	} else if t.Outdated {
		status = "outdated"
	}
	head := fmt.Sprintf("● %s %s · %s", t.ID, status, t.Where())
	if t.Resolved {
		add(head+" · "+strings.SplitN(t.Comments[0].Body, "\n", 2)[0], true)
		return rows
	}
	add(head, true)
	if t.Outdated {
		for _, l := range t.Lines {
			add("  "+l, false)
		}
	}
	wrap := lipgloss.NewStyle().Width(max(m.diffW()-16, 20))
	for _, c := range t.Comments {
		who := "you"
		if c.Author == Author {
			who = "claude"
		}
		if c.Pending {
			who += " (pending)"
		}
		add("  "+who+":", false)
		for _, l := range strings.Split(wrap.Render(c.Body), "\n") {
			add("    "+strings.TrimRight(l, " "), false)
		}
	}
	return rows
}

func (m *model) listW() int { return min(32, m.w/4) }
func (m *model) diffW() int { return m.w - m.listW() }

func (m *model) viewH() int {
	h := m.h - 2 // toolbar and status line
	if m.typing() {
		h -= m.input.Height()
	}
	return max(1, h)
}

func (m *model) View() string {
	if m.w == 0 {
		return ""
	}
	if m.d.state == nil {
		return "loading… " + m.msg
	}
	vh := m.viewH()
	if m.cur < m.top {
		m.top = m.cur
	}
	if m.cur >= m.top+vh {
		m.top = m.cur - vh + 1
	}

	// File list, scrolled so the current file stays visible.
	files, curPath, curIdx := m.files(), m.rows[m.cur].Path, 0
	for i, f := range files {
		if f.Path == curPath {
			curIdx = i
		}
	}
	lw, off := m.listW(), max(0, curIdx-vh+1)
	m.listOff = off
	lo, hi := m.cur, m.cur
	if m.mode == mVisual {
		lo, hi = min(m.anchor, m.cur), max(m.anchor, m.cur)
	}
	lines := make([]string, vh)
	for y := range lines {
		var b strings.Builder
		left, lst := "", stCtx
		if i := off + y; i < len(files) {
			left = " " + files[i].Path
			if files[i].Path == curPath {
				lst = stCurFile
			}
			if m.my == y+1 && m.mx < lw {
				lst = lst.Background(stHover.GetBackground())
			}
		}
		b.WriteString(lst.Width(lw-1).Render(ansi.Truncate(left, lw-1, "…")) + stDone.Render("│"))
		if i := m.top + y; i < len(m.rows) {
			b.WriteString(m.renderRow(i, i >= lo && i <= hi, m.my == y+1 && m.mx >= lw))
		} else {
			b.WriteString(stCtx.Width(m.diffW()).Render(""))
		}
		lines[y] = b.String()
	}
	if m.mode == mMenu {
		overlay(lines, m.menuView())
	} else if m.help {
		overlay(lines, m.helpView())
	}
	if m.typing() {
		lines = append(lines, stCtx.Width(m.w).Render(m.input.View()))
	}
	return strings.Join(append(append([]string{m.toolbar()}, lines...), m.status()), "\n")
}

func (m *model) renderRow(i int, selected, hovered bool) string {
	r := m.rows[i]
	num := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	var s, code string
	st := stCtx
	switch r.Kind {
	case KTop:
		s, st = r.Text, stTop
	case KFile:
		s, st = "▌ "+r.Path, stFile
	case KHunk, KMeta:
		s, st = r.Line.Text, stHunk
	case KThread:
		s, st = "           "+r.Text, stThread
		if r.Thread.Resolved {
			st = stDone
		}
	default:
		s, code = fmt.Sprintf("%4s %4s ", num(r.Line.Old), num(r.Line.New)), r.Line.Text
		if r.Kind == KAdd {
			st = stAdd.Background(bgAdd)
		} else if r.Kind == KDel {
			st = stDel.Background(bgDel)
		}
	}
	if i == m.cur {
		st = st.Background(bgCur)
	} else if selected && m.mode == mVisual {
		st = st.Background(stSel.GetBackground())
	} else if hovered {
		st = st.Background(stHover.GetBackground())
	}
	if code != "" {
		// Gutter and +/- marker keep the diff colors; the code itself gets token colors on the row's background.
		bg := st.GetBackground().(lipgloss.Color)
		s = stGutter.Background(bg).Render(s) + st.Render(code[:1])
		var toks []chroma.Token
		if h := m.hl[r.Path]; h != nil {
			toks = h.tokens(r.Path, r.Line)
		}
		if toks == nil {
			toks = []chroma.Token{{Type: chroma.Text, Value: code[1:]}}
		}
		for _, t := range toks {
			s += tokStyle(t.Type, bg).Render(strings.ReplaceAll(strings.TrimRight(t.Value, "\n"), "\t", "    "))
		}
	}
	w := m.diffW()
	return st.Width(w).Render(ansi.Truncate(s, w-1, "…"))
}

func (m *model) status() string {
	open, pending := 0, 0
	for _, t := range m.d.state.Threads {
		if !t.Resolved {
			open++
		}
		if t.Pending() {
			pending++
		}
	}
	author := "no Claude pane"
	if n := len(m.d.agents); n == 1 {
		author = "claude: " + m.d.agents[0].Status
	} else if n > 1 {
		author = fmt.Sprintf("%d Claude panes", n)
	}
	left := fmt.Sprintf(" %s │ %s vs %s │ %d open · %d pending │ %s │ ", modeNames[m.mode], m.repo.Branch, m.d.baseRef, open, pending, author)
	hint := m.msg
	if m.mode == mConfirm {
		hint = m.question
	}
	if hint == "" {
		switch m.mode {
		case mSubmit:
			hint = "submit review: (r)equest changes  (a)pprove  (esc) cancel"
		case mPick:
			var ps []string
			for i, a := range m.d.agents {
				ps = append(ps, fmt.Sprintf("%d) %s [%s]", i+1, a.Title, a.Status))
			}
			hint = strings.Join(ps, "  ")
		case mGit:
			hint = m.input.Placeholder + " · enter ok · ctrl+j newline · esc cancel"
			if m.redraft != nil {
				hint += " · ctrl+r ask Claude again"
			}
		case mMenu:
			hint = "j/k move · enter pick · esc close"
		case mInput, mSearch:
			hint = "enter save · ctrl+j newline · esc cancel"
		default:
			hint = "enter comment · v select · ? all keys"
		}
	}
	return stStatus.Width(m.w).Render(ansi.Truncate(left+hint, m.w, "…"))
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
