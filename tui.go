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
)

var modeNames = []string{"NORMAL", "VISUAL", "COMMENT", "SEARCH", "SUBMIT", "SEND TO"}

var (
	stTop     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	stFile    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	stHunk    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	stAdd     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	stDel     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stCtx     = lipgloss.NewStyle()
	stThread  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	stDone    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stSel     = lipgloss.NewStyle().Background(lipgloss.Color("238"))
	stHover   = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	stStatus  = lipgloss.NewStyle().Reverse(true)
	stCurFile = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
)

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
	base, tree string
	full, incr []File
	incrOK     bool
	state      *State
	raw        []byte
	agents     []Agent
	err        error
}

type tickMsg struct{}

type model struct {
	repo     *Repo
	baseFlag string
	d        loaded
	sig      string
	showFull bool
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
}

func newModel(r *Repo, base string) *model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = "│ "
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j"))
	ta.SetHeight(3)
	return &model{repo: r, baseFlag: base, input: ta, mx: -1, my: -1}
}

func (m *model) Init() tea.Cmd { return m.load }

// load runs off the UI goroutine; it only reads immutable fields.
func (m *model) load() tea.Msg {
	var d loaded
	if d.base, d.err = m.repo.Base(m.baseFlag); d.err != nil {
		return d
	}
	if d.tree, d.err = m.repo.Snapshot(); d.err != nil {
		return d
	}
	if d.full, d.err = m.repo.Diff(d.base, d.tree); d.err != nil {
		return d
	}
	if d.state, d.raw, d.err = loadState(m.repo.StatePath()); d.err != nil {
		return d
	}
	if d.state.LastReview != "" {
		// The snapshot tree may have been garbage-collected; then fall back to the full view.
		var err error
		d.incr, err = m.repo.Diff(d.state.LastReview, d.tree)
		d.incrOK = err == nil
	}
	d.agents, _ = agents(m.repo.Root)
	return d
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
		// ponytail: polls git every 1.5s; switch to inotify if big repos make this slow.
		next := tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
		if msg.err != nil {
			m.msg = msg.err.Error()
			return m, next
		}
		if sig := msg.base + msg.tree + string(msg.raw); sig != m.sig {
			m.d, m.sig = msg, sig
			m.rebuild()
		}
		m.d.agents = msg.agents
		return m, next
	case tickMsg:
		return m, m.load
	case tea.KeyMsg:
		return m, m.key(msg)
	case tea.MouseMsg:
		m.mouse(msg)
	}
	return m, nil
}

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
	if k.Type == tea.KeyRunes && len(k.Runes) > 1 && m.mode != mInput && m.mode != mSearch {
		var cmds []tea.Cmd
		for _, r := range k.Runes {
			cmds = append(cmds, m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}))
		}
		return tea.Batch(cmds...)
	}
	switch m.mode {
	case mInput, mSearch:
		switch s {
		case "esc":
			m.mode = mNormal
			m.input.Blur()
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
		if m.mode == mNormal && m.focus != "" {
			m.focus = ""
			m.rebuild()
		}
		m.mode = mNormal
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
	case "i":
		m.showFull = !m.showFull
		m.rebuild()
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
	switch e.Button {
	case tea.MouseButtonWheelUp:
		m.move(-3)
		return
	case tea.MouseButtonWheelDown:
		m.move(3)
		return
	}
	if (m.mode != mNormal && m.mode != mVisual) || e.Y >= m.viewH() {
		return
	}
	if e.X < m.listW() {
		if files := m.files(); e.Action == tea.MouseActionPress && e.Button == tea.MouseButtonLeft && m.listOff+e.Y < len(files) {
			p := files[m.listOff+e.Y].Path
			if m.focus == p {
				p = "" // clicking the focused file again shows all files
			}
			m.mode, m.focus = mNormal, p
			m.rebuild()
			for i, r := range m.rows {
				if r.Kind == KFile && r.Path == files[m.listOff+e.Y].Path {
					m.cur = i
				}
			}
		}
		return
	}
	i := m.top + e.Y
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
	for i := m.cur + dir; i >= 0 && i < len(m.rows); i += dir {
		if pred(m.rows[i]) {
			m.cur = i
			return
		}
	}
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
	m.mode, m.onInput = md, f
	m.input.Reset()
	m.input.Placeholder = placeholder
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
	tree := m.d.tree
	if m.mutate(func(s *State) error {
		for _, t := range s.Threads {
			for i := range t.Comments {
				t.Comments[i].Pending = false
			}
		}
		s.LastReview = tree
		return nil
	}) {
		m.msg = how
	}
}

func (m *model) approve() {
	tree := m.d.tree
	if m.mutate(func(s *State) error {
		s.Archived = append(s.Archived, s.Threads...)
		s.Threads, s.LastReview = nil, tree
		return nil
	}) {
		m.msg = "approved; threads archived"
	}
}

func (m *model) files() []File {
	if !m.showFull && m.d.incrOK {
		return m.d.incr
	}
	return m.d.full
}

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
	full := map[string][]Line{}
	for _, f := range m.d.full {
		full[f.Path] = f.Lines
	}
	for _, t := range st.Threads {
		t.Outdated = len(t.Lines) > 0 && findAnchor(full[t.Path], t) < 0
	}
	files := m.files()
	shown := map[string]bool{}
	for _, f := range files {
		shown[f.Path] = true
	}

	label := "Changeset vs " + m.d.base[:min(8, len(m.d.base))]
	if !m.showFull && m.d.incrOK {
		label = "Changes since last review"
	}
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
	h := m.h - 1
	if m.mode == mInput || m.mode == mSearch {
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
	var b strings.Builder
	for y := 0; y < vh; y++ {
		left, lst := "", lipgloss.NewStyle()
		if i := off + y; i < len(files) {
			left = " " + files[i].Path
			if files[i].Path == curPath {
				lst = stCurFile
			}
			if m.my == y && m.mx < lw {
				lst = lst.Inherit(stHover)
			}
		}
		b.WriteString(lst.Width(lw-1).Render(ansi.Truncate(left, lw-1, "…")) + "│")
		if i := m.top + y; i < len(m.rows) {
			b.WriteString(m.renderRow(i, i >= lo && i <= hi, m.my == y && m.mx >= lw))
		}
		b.WriteString("\n")
	}
	if m.mode == mInput || m.mode == mSearch {
		b.WriteString(m.input.View() + "\n")
	}
	b.WriteString(m.status())
	return b.String()
}

func (m *model) renderRow(i int, selected, hovered bool) string {
	r := m.rows[i]
	num := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	var s string
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
		s = fmt.Sprintf("%4s %4s ", num(r.Line.Old), num(r.Line.New)) + strings.ReplaceAll(r.Line.Text, "\t", "    ")
		if r.Kind == KAdd {
			st = stAdd
		} else if r.Kind == KDel {
			st = stDel
		}
	}
	w := m.diffW() - 1
	s = ansi.Truncate(s, w, "…")
	if i == m.cur {
		st = st.Reverse(true)
	} else if selected && m.mode == mVisual {
		st = st.Inherit(stSel)
	} else if hovered {
		st = st.Inherit(stHover)
	}
	return st.Width(w).Render(s)
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
	left := fmt.Sprintf(" %s │ %s │ %d open · %d pending │ %s │ ", modeNames[m.mode], m.repo.Branch, open, pending, author)
	hint := m.msg
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
		case mInput, mSearch:
			hint = "enter save · ctrl+j newline · esc cancel"
		default:
			hint = "enter comment · v select · R resolve · S submit · i full/new · / search · q quit"
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
