package main

import (
	"slices"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// themes is the `T` cycle; --theme also accepts any other chroma style name.
var themes = []string{"catppuccin-mocha", "catppuccin-macchiato", "catppuccin-frappe", "catppuccin-latte"}

var (
	theme   string
	chStyle *chroma.Style
	// Row backgrounds. Diff lines keep a tint so add/del stay visible under the token colors.
	bgAdd, bgDel, bgCur lipgloss.Color
	stGutter            lipgloss.Style
)

func init() { setTheme(themes[0]) }

// setTheme restyles the whole UI from a chroma style. Unknown names fall back to the default and return false.
func setTheme(name string) bool {
	s, ok := styles.Registry[name]
	if !ok {
		name, s = themes[0], styles.Registry[themes[0]]
	}
	theme, chStyle = name, s
	bgE := s.Get(chroma.Background)
	bg, fg := bgE.Background, bgE.Colour
	if !bg.IsSet() {
		bg = chroma.MustParseColour("#000000")
	}
	if !fg.IsSet() {
		fg = chroma.MustParseColour("#ffffff")
	}
	pick := func(t chroma.TokenType) chroma.Colour {
		if c := s.Get(t).Colour; c.IsSet() {
			return c
		}
		return fg
	}
	c := func(x chroma.Colour) lipgloss.Color { return lipgloss.Color(x.String()) }
	blue, green, red := pick(chroma.NameFunction), pick(chroma.GenericInserted), pick(chroma.GenericDeleted)
	dim := pick(chroma.Comment)

	bgAdd, bgDel = c(mix(bg, green, 0.18)), c(mix(bg, red, 0.18))
	bgCur = c(mix(bg, fg, 0.22))
	base := lipgloss.NewStyle().Background(c(bg)).Foreground(c(fg))
	stCtx = base
	stTop = base.Bold(true).Foreground(c(pick(chroma.Keyword)))
	stFile = base.Bold(true).Foreground(c(blue)).Background(c(mix(bg, blue, 0.12)))
	stHunk = base.Foreground(c(pick(chroma.NameBuiltin)))
	stAdd = base.Foreground(c(green))
	stDel = base.Foreground(c(red))
	stThread = base.Foreground(c(pick(chroma.NameClass)))
	stDone = base.Foreground(c(dim))
	stGutter = stDone
	stSel = lipgloss.NewStyle().Background(c(mix(bg, blue, 0.25)))
	stHover = lipgloss.NewStyle().Background(c(mix(bg, fg, 0.10)))
	stStatus = base.Background(c(mix(bg, fg, 0.14)))
	stCurFile = base.Bold(true).Foreground(c(blue))
	stBtn = base.Bold(true).Foreground(c(bg)).Background(c(blue))
	stBtnHov = stBtn.Background(c(mix(blue, fg, 0.35)))
	stBtnOff = base.Foreground(c(dim)).Background(c(mix(bg, fg, 0.22)))
	stMenu = base.Border(lipgloss.RoundedBorder()).BorderForeground(c(blue)).BorderBackground(c(bg))
	return ok
}

// nextTheme cycles through themes; a custom --theme restarts the cycle.
func nextTheme() string {
	setTheme(themes[(slices.Index(themes, theme)+1)%len(themes)])
	return theme
}

func mix(a, b chroma.Colour, t float64) chroma.Colour {
	m := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t) }
	return chroma.NewColour(m(a.Red(), b.Red()), m(a.Green(), b.Green()), m(a.Blue(), b.Blue()))
}

// tokStyle is the current theme's style for a token, on background bg.
func tokStyle(t chroma.TokenType, bg lipgloss.Color) lipgloss.Style {
	e := chStyle.Get(t)
	st := stCtx.Background(bg).Bold(e.Bold == chroma.Yes).Italic(e.Italic == chroma.Yes).Underline(e.Underline == chroma.Yes)
	if e.Colour.IsSet() {
		st = st.Foreground(lipgloss.Color(e.Colour.String()))
	}
	return st
}

// highlight holds the syntax tokens of one File's diff, by line number on each side.
type highlight struct {
	lines    []Line
	old, new map[int][]chroma.Token
}

// tokens returns the tokens of a code line, tokenizing the File on first use.
func (h *highlight) tokens(path string, l Line) []chroma.Token {
	if h.old == nil {
		h.old, h.new = h.side(path, true), h.side(path, false)
	}
	if l.Kind == KDel {
		return h.old[l.Old]
	}
	return h.new[l.New]
}

// side tokenizes all lines of one side in one pass, so strings and comments spanning lines color right.
// ponytail: hunks are joined back to back, so a token spanning a gap between hunks can miscolor; read whole blobs if that bites.
func (h *highlight) side(path string, old bool) map[int][]chroma.Token {
	var src strings.Builder
	var nums []int
	for _, l := range h.lines {
		n := l.New
		if old {
			n = l.Old
		}
		if l.code() && n != 0 {
			src.WriteString(l.Text[1:] + "\n")
			nums = append(nums, n)
		}
	}
	lx := lexers.Match(path)
	if lx == nil {
		lx = lexers.Analyse(src.String())
	}
	if lx == nil {
		lx = lexers.Fallback
	}
	out := map[int][]chroma.Token{}
	it, err := chroma.Coalesce(lx).Tokenise(nil, src.String())
	if err != nil {
		return out
	}
	for i, toks := range chroma.SplitTokensIntoLines(it.Tokens()) {
		if i < len(nums) {
			out[nums[i]] = toks
		}
	}
	return out
}
