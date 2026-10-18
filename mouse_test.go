package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestMouse(t *testing.T) {
	m := newModel(nil, "")
	m.w, m.h, m.d.state = 100, 20, &State{}
	m.rows = []Row{{Kind: KTop}, {Kind: KFile, Path: "a"}}
	for n := 1; n <= 3; n++ {
		m.rows = append(m.rows, Row{Kind: KAdd, Path: "a", Line: Line{Kind: KAdd, New: n, Text: "x"}})
	}
	ev := func(a tea.MouseAction, b tea.MouseButton, y int) {
		m.mouse(tea.MouseMsg{X: 50, Y: y, Action: a, Button: b})
	}
	click := func(y int) {
		ev(tea.MouseActionPress, tea.MouseButtonLeft, y)
		ev(tea.MouseActionRelease, tea.MouseButtonNone, y)
	}

	// Screen row y shows row y-1: the toolbar is row 0.
	click(3)
	if m.cur != 2 || m.mode != mNormal {
		t.Fatalf("click selects: cur=%d mode=%d", m.cur, m.mode)
	}
	click(3)
	if m.mode != mInput {
		t.Fatalf("second click comments: mode=%d", m.mode)
	}
	m.mode = mNormal
	ev(tea.MouseActionPress, tea.MouseButtonLeft, 4)
	ev(tea.MouseActionMotion, tea.MouseButtonLeft, 5)
	ev(tea.MouseActionRelease, tea.MouseButtonNone, 5)
	if m.mode != mVisual || m.anchor != 3 || m.cur != 4 {
		t.Fatalf("drag selects: mode=%d anchor=%d cur=%d", m.mode, m.anchor, m.cur)
	}
}

// The wheel scrolls the view and leaves the cursor; moving the cursor brings it back into view.
func TestWheelScroll(t *testing.T) {
	m := newModel(nil, "")
	m.w, m.h, m.d.state = 100, 12, &State{} // 10 rows of view
	m.rows = []Row{{Kind: KTop}, {Kind: KFile, Path: "a"}}
	for n := 1; n <= 28; n++ {
		m.rows = append(m.rows, Row{Kind: KAdd, Path: "a", Line: Line{Kind: KAdd, New: n, Text: "x"}})
	}
	send := func(msg tea.Msg) { m.Update(msg) }
	wheel := func(b tea.MouseButton, n int) {
		for range n {
			send(tea.MouseMsg{X: 50, Y: 5, Action: tea.MouseActionPress, Button: b})
		}
	}
	m.cur = 2
	if wheel(tea.MouseButtonWheelDown, 2); m.top != 6 || m.cur != 2 {
		t.Fatalf("wheel down: top=%d cur=%d", m.top, m.cur)
	}
	if wheel(tea.MouseButtonWheelDown, 10); m.top != 20 {
		t.Fatalf("clamped at the bottom: top=%d", m.top)
	}
	if wheel(tea.MouseButtonWheelUp, 10); m.top != 0 || m.cur != 2 {
		t.Fatalf("clamped at the top: top=%d cur=%d", m.top, m.cur)
	}
	wheel(tea.MouseButtonWheelDown, 4) // top 12, cursor off screen
	if send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}); m.cur != 3 || m.top != 3 {
		t.Fatalf("j snaps back: top=%d cur=%d", m.top, m.cur)
	}
	wheel(tea.MouseButtonWheelDown, 2) // top 9
	send(tea.MouseMsg{X: 50, Y: 4, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.cur != 12 || m.top != 9 {
		t.Fatalf("click picks the row under the mouse: top=%d cur=%d", m.top, m.cur)
	}
	wheel(tea.MouseButtonWheelDown, 3)
	if send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}}); m.top != 18 {
		t.Fatalf("a key that leaves the cursor keeps the scroll: top=%d", m.top)
	}
}

func TestReloadKeepsScroll(t *testing.T) {
	m := newModel(nil, "")
	m.w, m.h = 100, 12
	var lines []Line
	for n := 1; n <= 30; n++ {
		lines = append(lines, Line{Kind: KAdd, New: n, Text: "x"})
	}
	d := loaded{once: true, base: "abc", state: &State{}, files: []File{{Path: "a", Lines: lines}}}
	m.Update(d)
	m.Update(tea.MouseMsg{X: 50, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	d.raw = []byte("changed")
	if m.Update(d); m.top != 3 || m.cur != 0 {
		t.Fatalf("reload: top=%d cur=%d", m.top, m.cur)
	}
}

func TestFocusFile(t *testing.T) {
	m := newModel(nil, "")
	m.w, m.h, m.d.state, m.d.base = 100, 20, &State{}, "abc"
	m.d.files = []File{{Path: "a", Lines: []Line{{Kind: KAdd, New: 1, Text: "x"}}}, {Path: "b", Lines: []Line{{Kind: KAdd, New: 1, Text: "y"}}}}
	m.build()
	m.mouse(tea.MouseMsg{X: 1, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.focus != "b" || len(m.rows) != 3 || m.rows[m.cur].Path != "b" {
		t.Fatalf("focus b: focus=%q rows=%d cur=%d", m.focus, len(m.rows), m.cur)
	}
	m.mouse(tea.MouseMsg{X: 1, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.focus != "" || len(m.rows) != 6 || m.rows[3].Kind != KGap {
		t.Fatalf("unfocus: focus=%q rows=%d", m.focus, len(m.rows))
	}
}

// The spacer between two files is skipped by keys, clicks and drags, and shows in the header.
func TestGap(t *testing.T) {
	m := newModel(nil, "")
	m.w, m.h, m.d.state, m.d.base = 100, 20, &State{}, "abc"
	m.d.files = []File{{Path: "a", Status: "modified", Lines: []Line{{Kind: KAdd, New: 1, Text: "+x"}}}, {Path: "d/b", Status: "new", Lines: []Line{{Kind: KAdd, New: 1, Text: "+y"}}}}
	m.build() // top, a, +x, gap, d/b, +y
	m.cur = 2
	if m.move(1); m.cur != 4 {
		t.Fatalf("down skips the gap: cur=%d", m.cur)
	}
	if m.move(-1); m.cur != 2 {
		t.Fatalf("up skips the gap: cur=%d", m.cur)
	}
	for _, a := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionMotion, tea.MouseActionRelease} {
		m.mouse(tea.MouseMsg{X: 50, Y: 4, Action: a, Button: tea.MouseButtonLeft})
	}
	if m.cur != 2 || m.mode != mNormal {
		t.Fatalf("clicking the gap does nothing: cur=%d mode=%d", m.cur, m.mode)
	}
	m.cur = 4
	if h := ansi.Strip(m.renderRow(4, false, false)); !strings.Contains(h, "▌ d/b") || !strings.HasSuffix(strings.TrimRight(h, " "), "new  +1 −0") {
		t.Fatalf("header: %q", h)
	}
}
