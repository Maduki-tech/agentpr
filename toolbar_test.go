package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// clickTool draws the toolbar and clicks the button that presses key; false if it isn't shown.
func clickTool(m *model, key string) bool {
	m.toolbar()
	for _, t := range m.bar {
		if t.key.String() == key && t.w > 0 {
			m.mouse(tea.MouseMsg{X: t.x, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			return true
		}
	}
	return false
}

func toolModel() *model {
	m := newModel(&Repo{Branch: "f"}, "")
	m.w, m.h, m.d.state, m.d.base, m.d.baseRef = 120, 30, &State{}, "abc", "main"
	m.d.files = []File{{Path: "a", Lines: []Line{{Kind: KAdd, New: 1, Text: "+x"}}}, {Path: "b", Lines: []Line{{Kind: KAdd, New: 1, Text: "+y"}}}}
	m.build()
	return m
}

func TestToolbarClicks(t *testing.T) {
	defer setTheme(themes[0])
	m := toolModel()
	if !clickTool(m, "]f") || m.rows[m.cur].Kind != KFile || m.rows[m.cur].Path != "a" {
		t.Fatalf("next file: cur=%d", m.cur)
	}
	if !clickTool(m, "T") || theme != themes[1] {
		t.Fatalf("theme cycles: %q", theme)
	}
	if !clickTool(m, "?") || !m.help {
		t.Fatal("keys shows the help")
	}
	// Disabled: nothing pending, so a click must not even say so.
	if !clickTool(m, "Sr") || m.msg != "" || m.mode != mNormal {
		t.Fatalf("disabled button ran: msg=%q mode=%d", m.msg, m.mode)
	}
	if !clickTool(m, "enter") || m.mode != mInput {
		t.Fatalf("comment opens the box: mode=%d", m.mode)
	}
	// Buttons are off while typing, so clicks don't land in the box.
	if clickTool(m, "/"); m.mode != mInput {
		t.Fatalf("click while typing: mode=%d", m.mode)
	}
	m.mode = mNormal
	if !clickTool(m, "/") || m.mode != mSearch {
		t.Fatalf("search: mode=%d", m.mode)
	}
	m.mode, m.cmd = mNormal, nil
	if !clickTool(m, "q") || m.cmd == nil {
		t.Fatal("quit")
	}
	if _, ok := m.cmd().(tea.QuitMsg); !ok {
		t.Fatal("quit sends QuitMsg")
	}
}

func TestToolbarFits(t *testing.T) {
	m := toolModel()
	for _, w := range []int{30, 60, 80, 120, 200} {
		m.w = w
		if got := lipgloss.Width(m.toolbar()); got != w {
			t.Errorf("width %d: toolbar is %d wide", w, got)
		}
		shown := ""
		for _, b := range m.bar {
			if b.w > 0 && b.x+b.w > w {
				t.Errorf("width %d: %q ends at %d", w, b.label, b.x+b.w)
			}
			if b.w > 0 {
				shown += b.label + "|"
			}
		}
		if !strings.Contains(shown, "✕") || !strings.Contains(shown, "▾") {
			t.Errorf("width %d: Quit or the git menu dropped: %s", w, shown)
		}
		if w == 200 && !strings.Contains(shown, "Request changes") {
			t.Errorf("width 200 shortened: %s", shown)
		}
		lines := strings.Split(m.View(), "\n")
		if len(lines) != m.h {
			t.Errorf("width %d: %d lines, want %d", w, len(lines), m.h)
		}
		for i, l := range lines {
			if lipgloss.Width(l) != w {
				t.Errorf("width %d: line %d is %d wide", w, i, lipgloss.Width(l))
			}
		}
	}
}

func TestHelpDialog(t *testing.T) {
	m := toolModel()
	press(m, "?")
	box, x, y := m.helpBox()
	if box == nil {
		t.Fatal("no box")
	}
	bw := lipgloss.Width(box[0])
	if x != (m.w-bw)/2 || y != 1+(m.viewH()-len(box))/2 {
		t.Fatalf("not centred: x=%d y=%d box=%dx%d", x, y, bw, len(box))
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Keyboard shortcuts") || !strings.Contains(v, "esc or ? to close") {
		t.Fatal("title or hint missing")
	}
	for _, k := range []string{"q", "esc", "?"} {
		press(m, "?")
		if press(m, k); m.help {
			t.Fatalf("%q does not close", k)
		}
	}
	// Clicking inside keeps it open, outside closes it; the toolbar is not clicked through.
	press(m, "?")
	m.mouse(tea.MouseMsg{X: x + 2, Y: y + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !m.help {
		t.Fatal("click inside closed it")
	}
	m.mouse(tea.MouseMsg{X: 0, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.help {
		t.Fatal("click outside kept it")
	}
	// Tiny terminals: no panic, still closable.
	for _, sz := range [][2]int{{20, 8}, {5, 3}, {1, 1}} {
		m.w, m.h = sz[0], sz[1]
		press(m, "?")
		m.View()
		press(m, "q")
		if m.help {
			t.Fatalf("%v: q did not close", sz)
		}
	}
}
