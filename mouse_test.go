package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
	ev(tea.MouseActionPress, tea.MouseButtonWheelUp, 0)
	if m.cur != 1 {
		t.Fatalf("wheel scrolls: cur=%d", m.cur)
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
	if m.focus != "" || len(m.rows) != 5 {
		t.Fatalf("unfocus: focus=%q rows=%d", m.focus, len(m.rows))
	}
}
