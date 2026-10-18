package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestTheme(t *testing.T) {
	defer setTheme(themes[0])
	if setTheme("no-such-theme") || theme != themes[0] {
		t.Fatalf("unknown theme should fall back: %q", theme)
	}
	if !setTheme("catppuccin-latte") || nextTheme() != themes[0] {
		t.Fatalf("cycle: %q", theme)
	}

	lipgloss.SetColorProfile(termenv.TrueColor)
	fs := parseDiff("diff --git a/a.go b/a.go\n+++ b/a.go\n@@ -1,0 +1,3 @@\n+func f() {\n+\t/* a\n+\tb */\n")
	m := newModel(nil, "")
	m.w, m.h, m.d.state, m.d.base, m.showFull, m.d.full = 100, 20, &State{}, "abc", true, fs
	m.build()
	out := m.renderRow(3, false, false)
	// "func" in catppuccin-mocha's declaration-keyword red, on the add tint.
	if !strings.Contains(out, "38;2;243;139;168") || !strings.Contains(out, "func") {
		t.Fatalf("no keyword color: %q", out)
	}
	// The open comment's second line is still colored as a comment (whole side tokenized at once).
	if out := m.renderRow(5, false, false); !strings.Contains(out, "38;2;108;112;134") {
		t.Fatalf("no comment color: %q", out)
	}
}
