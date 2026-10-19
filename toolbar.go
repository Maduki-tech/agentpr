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

var helpKeys = [][2]string{
	{"j k  ^d ^u  gg G", "move"},
	{"]c [c  ]f [f  ]t [t", "next / previous hunk, file, Thread"},
	{"v V", "select lines"},
	{"enter", "comment; reply on a Thread"},
	{"R", "resolve / unresolve"},
	{"S r  S a", "request changes / approve"},
	{"c  C", "git action / git menu"},
	{"b", "compare against another branch (/ filters)"},
	{"T", "next theme"},
	{"/  n N", "search, next / previous"},
	{"esc", "all files; close this"},
	{"?", "this list"},
	{"q", "quit"},
}

// helpView is the key list box, drawn like the git menu.
func (m *model) helpView() []string {
	lines := []string{stCtx.Bold(true).Render(" Keys")}
	for _, k := range helpKeys {
		lines = append(lines, stCtx.Render(fmt.Sprintf(" %-20s %s ", k[0], k[1])))
	}
	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}
	w = min(w, m.w-2)
	for i, l := range lines {
		lines[i] = stCtx.Width(w).Render(ansi.Truncate(l, w, "…"))
	}
	return strings.Split(stMenu.Render(strings.Join(lines, "\n")), "\n")
}
