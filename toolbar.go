package main

// The toolbar on row 0: a button for each action. A click presses the button's key, so
// buttons and keys always do the same thing.

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Set from the active theme by setTheme.
var stTool, stToolHov lipgloss.Style

type tool struct {
	label, short string // short replaces label when the row is too narrow
	key          tea.KeyMsg
	on           bool
	pri          int  // narrow rows shorten, then drop, the lowest first
	git          bool // part of the git split button, styled as the primary action
	glued        bool // no gap before it: the split button's ▾
	x, w         int  // set by toolbar; w is 0 when dropped
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// toolbar renders row 0 and records where each button is for the mouse.
func (m *model) toolbar() string {
	r, live := m.rows[m.cur], m.mode == mNormal || m.mode == mVisual
	file := func(dir int) bool { return m.next(dir, func(r Row) bool { return r.Kind == KFile }) >= 0 }
	label, a, _ := quick(m.gitState())
	if m.busy != "" {
		label, a = m.busy, aNone
	}
	left := []tool{
		{label: "‹ File", short: "‹", key: runes("[f"), on: file(-1), pri: 5},
		{label: "File ›", short: "›", key: runes("]f"), on: file(1), pri: 5},
		{label: "💬 Comment", short: "💬", key: tea.KeyMsg{Type: tea.KeyEnter}, pri: 8,
			on: m.mode == mVisual || r.Thread != nil || r.Line.code() || r.Kind == KFile || r.Kind == KTop},
		{label: "✓ Resolve", short: "✓", key: runes("R"), on: r.Thread != nil, pri: 7},
		{label: "Request changes", short: "Request", key: runes("Sr"), on: slices.ContainsFunc(m.d.state.Threads, (*Thread).Pending), pri: 6},
		{label: "Approve", short: "Approve", key: runes("Sa"), on: true, pri: 6},
		{label: "Base: " + m.d.baseRef + " ▾", short: "Base ▾", key: runes("b"), on: true, pri: 4},
		{label: "/ Search", short: "/", key: runes("/"), on: true, pri: 3},
		{label: "◐ " + strings.TrimPrefix(theme, "catppuccin-"), short: "◐", key: runes("T"), on: true, pri: 1},
		{label: "? Keys", short: "?", key: runes("?"), on: true, pri: 2},
	}
	right := []tool{
		{label: "⎇ " + label, short: "⎇", key: runes("c"), on: a != aNone, pri: 9, git: true},
		{label: "▾", short: "▾", key: runes("C"), on: true, pri: 9, git: true, glued: true},
		{label: "✕ Quit", short: "✕", key: runes("q"), on: true, pri: 10},
	}
	all := append(left, right...)
	width := func(ts []tool) (n int) {
		for _, t := range ts {
			if t.label != "" {
				n += lipgloss.Width(t.label) + 3 // padding, and a gap or the ▾'s │
			}
		}
		return n
	}
	order := make([]*tool, len(all))
	for i := range all {
		order[i] = &all[i]
	}
	slices.SortStableFunc(order, func(a, b *tool) int { return a.pri - b.pri })
	for _, t := range order {
		if width(all) > m.w {
			t.label = t.short
		}
	}
	for _, t := range order {
		if width(all) > m.w {
			t.label = ""
		}
	}

	var b strings.Builder
	x := 0
	for i := range all {
		t := &all[i]
		if i == len(left) { // right-align the git button and Quit
			fill := max(0, m.w-x-width(all[i:]))
			b.WriteString(stStatus.Render(strings.Repeat(" ", fill)))
			x += fill
		}
		t.on = t.on && live
		if t.label == "" {
			continue
		}
		text := " " + t.label + " "
		if t.glued {
			text = "│" + text
		} else {
			b.WriteString(stStatus.Render(" "))
			x++
		}
		t.x, t.w = x, lipgloss.Width(text)
		x += t.w
		st, hov := stTool, stToolHov
		if t.git {
			st, hov = stBtn, stBtnHov
		}
		if !t.on {
			st = stBtnOff
		} else if m.my == 0 && m.mx >= t.x && m.mx < t.x+t.w {
			st = hov
		}
		b.WriteString(st.Render(text))
	}
	m.bar = all
	return ansi.Truncate(b.String(), m.w, "")
}

// toolClick presses the key of the enabled button at column x.
func (m *model) toolClick(x int) {
	for _, t := range m.bar {
		if t.on && t.w > 0 && x >= t.x && x < t.x+t.w {
			cmd := m.key(t.key)
			m.cmd = tea.Batch(m.cmd, cmd)
		}
	}
}

// helpKeys: an entry with no description is a section heading.
var helpKeys = [][2]string{
	{"Navigate", ""},
	{"j k  ^d ^u  gg G", "move"},
	{"]c [c  ]f [f  ]t [t", "next / previous hunk, file, Thread"},
	{"/  n N", "search, next / previous"},
	{"esc", "all files; close this"},
	{"Review", ""},
	{"v V", "select lines"},
	{"enter", "comment; reply on a Thread"},
	{"R", "resolve / unresolve"},
	{"S r  S a", "request changes / approve"},
	{"Git", ""},
	{"c  C", "git action / git menu"},
	{"b", "compare against another branch (/ filters)"},
	{"View", ""},
	{"T", "next theme"},
	{"?", "this list"},
	{"q", "quit"},
}

// helpBox is the centred key dialog and its top left corner on screen; nil if the screen is too small.
func (m *model) helpBox() (box []string, x, y int) {
	w := 0
	for _, k := range helpKeys {
		w = max(w, len(k[0]))
	}
	kw := min(w, 20)
	content := []string{stTop.Render("Keyboard shortcuts"), ""}
	for _, k := range helpKeys {
		switch {
		case k[1] == "" && len(content) > 2:
			content = append(content, "", stTop.Render(k[0]))
		case k[1] == "":
			content = append(content, stTop.Render(k[0]))
		default:
			content = append(content, stCurFile.Render(fmt.Sprintf("%-*s", kw, k[0]))+"  "+stCtx.Render(k[1]))
		}
	}
	// 2 for the border, 2 for the padding on each side
	iw, ih := m.w-4-2, m.viewH()-2-2
	if iw < 1 || ih < 1 {
		return nil, 0, 0
	}
	content = append(content[:max(0, min(len(content), ih-2))], "", stDone.Render("esc or ? to close")) // the hint always stays
	content = content[max(0, len(content)-ih):]
	w = 0
	for _, l := range content {
		w = max(w, lipgloss.Width(l))
	}
	w = min(w, iw)
	for i, l := range content {
		content[i] = stCtx.Width(w).Render(ansi.Truncate(l, w, "…"))
	}
	box = strings.Split(stMenu.Padding(1, 2).Render(strings.Join(content, "\n")), "\n")
	x = (m.w - lipgloss.Width(box[0])) / 2
	y = 1 + (m.viewH()-len(box))/2 // below the toolbar
	return box, x, y
}
