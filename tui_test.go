package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// reviewModel is toolModel with a state dir of its own, so mutations never touch the working directory.
func reviewModel(t *testing.T) *model {
	m := toolModel()
	m.repo = &Repo{GitDir: t.TempDir(), Branch: "f"}
	return m
}

func press(m *model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

// say types text into the open input box and submits it.
func say(m *model, text string) {
	m.input.SetValue(text)
	press(m, "enter")
}

func stored(t *testing.T, m *model) *State {
	s, _, err := loadState(m.repo.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// onRow moves the cursor to the first row matching pred.
func onRow(t *testing.T, m *model, pred func(Row) bool) {
	for i, r := range m.rows {
		if pred(r) {
			m.cur = i
			return
		}
	}
	t.Fatal("no such row")
}

func onLine(t *testing.T, m *model, text string) {
	onRow(t, m, func(r Row) bool { return r.Line.Text == text })
}

func TestCommentOnLineThenReplyAndResolve(t *testing.T) {
	m := reviewModel(t)
	onLine(t, m, "+x")
	press(m, "enter")
	if m.mode != mInput || m.input.Placeholder != "comment on a:1" {
		t.Fatalf("mode=%d placeholder=%q", m.mode, m.input.Placeholder)
	}
	say(m, "  needs a test  ")
	s := stored(t, m)
	if len(s.Threads) != 1 || s.NextID != 1 {
		t.Fatalf("state: %+v", s)
	}
	th := s.Threads[0]
	if th.ID != "t1" || th.Path != "a" || th.Line != 1 || th.Lines[0] != "+x" ||
		th.Comments[0].Body != "needs a test" || th.Comments[0].Author != Reviewer || !th.Comments[0].Pending {
		t.Fatalf("thread: %+v", th)
	}
	if m.d.state.Thread("t1") == nil {
		t.Fatal("model state not refreshed")
	}

	// The Thread shows inline after its line; Enter there replies.
	onRow(t, m, func(r Row) bool { return r.Thread != nil })
	press(m, "enter")
	if m.mode != mInput || m.input.Placeholder != "reply to t1" {
		t.Fatalf("reply box: mode=%d %q", m.mode, m.input.Placeholder)
	}
	say(m, "also this")
	if got := stored(t, m).Threads[0]; len(got.Comments) != 2 || got.Comments[1].Body != "also this" {
		t.Fatalf("reply: %+v", got.Comments)
	}

	// R resolves, a reply reopens.
	press(m, "R")
	if !stored(t, m).Threads[0].Resolved {
		t.Fatal("R must resolve")
	}
	onRow(t, m, func(r Row) bool { return r.Thread != nil })
	press(m, "R")
	if stored(t, m).Threads[0].Resolved {
		t.Fatal("R again must reopen")
	}
	press(m, "enter")
	say(m, "one more")
	if stored(t, m).Threads[0].Resolved {
		t.Fatal("reply keeps it open")
	}
}

func TestReplyToVanishedThread(t *testing.T) {
	m := reviewModel(t)
	onLine(t, m, "+x")
	press(m, "enter")
	say(m, "c")
	onRow(t, m, func(r Row) bool { return r.Thread != nil })
	press(m, "enter")
	updateState(m.repo.StatePath(), func(s *State) error { s.Threads = nil; return nil })
	say(m, "hello?")
	if m.msg != "thread t1 is gone" {
		t.Fatalf("msg=%q", m.msg)
	}
}

func TestCommentAnchors(t *testing.T) {
	m := reviewModel(t)
	// Visual selection over two lines of one file.
	m.d.files[0].Lines = append(m.d.files[0].Lines, Line{Kind: KAdd, New: 2, Text: "+x2"})
	m.build()
	onLine(t, m, "+x")
	press(m, "v", "j", "enter")
	if m.mode != mInput || m.input.Placeholder != "comment on a:1-2" {
		t.Fatalf("mode=%d %q", m.mode, m.input.Placeholder)
	}
	say(m, "two lines")
	// File header and Changeset header.
	onRow(t, m, func(r Row) bool { return r.Kind == KFile && r.Path == "b" })
	press(m, "enter")
	say(m, "file")
	m.cur = 0
	press(m, "enter")
	if m.input.Placeholder != "comment on (whole changeset)" {
		t.Fatalf("placeholder %q", m.input.Placeholder)
	}
	say(m, "overall")
	ts := stored(t, m).Threads
	if len(ts) != 3 || ts[0].Where() != "a:1-2" || ts[1].Where() != "b" || ts[2].Where() != "(whole changeset)" {
		t.Fatalf("threads: %v %v %v", len(ts), ts[0].Where(), ts[len(ts)-1].Where())
	}
	// Esc cancels without writing.
	m.cur = 0
	press(m, "enter", "esc")
	if m.mode != mNormal || len(stored(t, m).Threads) != 3 {
		t.Fatal("esc must cancel")
	}
}

func TestMutateError(t *testing.T) {
	m := reviewModel(t)
	m.repo.GitDir = filepath.Join(m.repo.GitDir, "file")
	os.WriteFile(m.repo.GitDir, nil, 0o644) // a file where the state dir should go
	if m.mutate(func(*State) error { return nil }) || m.msg == "" {
		t.Fatalf("msg=%q", m.msg)
	}
}

func TestSearch(t *testing.T) {
	m := toolModel()
	press(m, "/")
	if m.mode != mSearch {
		t.Fatalf("mode=%d", m.mode)
	}
	say(m, "+Y") // case-insensitive
	if m.rows[m.cur].Line.Text != "+y" {
		t.Fatalf("cur on %+v", m.rows[m.cur])
	}
	press(m, "n") // wraps around: only one match
	if m.rows[m.cur].Line.Text != "+y" || m.msg != "" {
		t.Fatalf("n: %+v msg=%q", m.rows[m.cur], m.msg)
	}
	m.search = "zzz"
	press(m, "N")
	if m.msg != "not found: zzz" {
		t.Fatalf("msg=%q", m.msg)
	}
	m.search, m.msg = "", ""
	cur := m.cur
	press(m, "n")
	if m.cur != cur || m.msg != "" {
		t.Fatal("empty search is a no-op")
	}
	m.search = "+x"
	press(m, "N")
	if m.rows[m.cur].Line.Text != "+x" {
		t.Fatalf("N backwards: %+v", m.rows[m.cur])
	}
}

func pendingThread(m *model, t *testing.T, line string) {
	onLine(t, m, line)
	press(m, "enter")
	say(m, "fix "+line)
}

func TestRequestChangesNothingPending(t *testing.T) {
	m := reviewModel(t)
	m.requestChanges()
	if m.msg != "nothing pending to send" || m.prompt != "" {
		t.Fatalf("msg=%q", m.msg)
	}
}

func TestSubmitFallsBackToFile(t *testing.T) {
	fakeBin(t, nil) // no herdr, wl-copy or xclip
	m := reviewModel(t)
	pendingThread(m, t, "+x")
	press(m, "S")
	if m.mode != mSubmit {
		t.Fatalf("mode=%d", m.mode)
	}
	press(m, "r")
	p := filepath.Join(m.repo.GitDir, "agentpr", "last-review.md")
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "fix +x") {
		t.Fatalf("last-review.md: %q %v", b, err)
	}
	if !strings.Contains(m.msg, "review written to "+p) {
		t.Fatalf("msg=%q", m.msg)
	}
	if stored(t, m).Threads[0].Pending() {
		t.Fatal("delivered comments must stop being pending")
	}
}

func TestDeliverToAgent(t *testing.T) {
	dir := fakeBin(t, map[string]string{"herdr": `echo "$@" > @OUT@`})
	m := reviewModel(t)
	m.d.agents = []Agent{{Pane: "p9", Status: "working"}}
	pendingThread(m, t, "+x")
	press(m, "S", "r")
	if b, _ := os.ReadFile(filepath.Join(dir, "out")); !strings.HasPrefix(string(b), "agent prompt p9 Code review from agentpr") {
		t.Fatalf("herdr args: %q", b)
	}
	if want := "review sent to Claude (busy; queued for after this turn)"; m.msg != want {
		t.Fatalf("msg=%q", m.msg)
	}
	if stored(t, m).Threads[0].Pending() {
		t.Fatal("still pending")
	}
}

func TestDeliverClipboardWhenHerdrFails(t *testing.T) {
	dir := fakeBin(t, map[string]string{
		"herdr":   "echo gone; exit 1",
		"wl-copy": `while read -r l || [ -n "$l" ]; do echo "$l"; done > @OUT@`,
	})
	m := reviewModel(t)
	m.d.agents = []Agent{{Pane: "p1"}}
	pendingThread(m, t, "+x")
	press(m, "S", "r")
	if want := "herdr: gone; review copied to clipboard"; m.msg != want {
		t.Fatalf("msg=%q", m.msg)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out")); !strings.Contains(string(b), "fix +x") {
		t.Fatalf("clipboard: %q", b)
	}
}

func TestDeliverFileWriteFails(t *testing.T) {
	fakeBin(t, nil)
	m := reviewModel(t)
	pendingThread(m, t, "+x")
	m.prompt = "p"
	os.RemoveAll(filepath.Join(m.repo.GitDir, "agentpr"))
	os.WriteFile(filepath.Join(m.repo.GitDir, "agentpr"), nil, 0o644)
	m.deliver(Agent{})
	if !strings.HasPrefix(m.msg, "could not deliver review: ") {
		t.Fatalf("msg=%q", m.msg)
	}
}

func TestPickAmongSeveralAgents(t *testing.T) {
	dir := fakeBin(t, map[string]string{"herdr": `echo "$@" > @OUT@`})
	m := reviewModel(t)
	m.d.agents = []Agent{{Pane: "p1", Title: "one"}, {Pane: "p2", Title: "two"}}
	pendingThread(m, t, "+x")
	press(m, "S", "r")
	if m.mode != mPick || !strings.Contains(m.status(), "1) one") {
		t.Fatalf("mode=%d status=%q", m.mode, m.status())
	}
	press(m, "9") // out of range
	if m.msg != "review not sent" || !stored(t, m).Threads[0].Pending() {
		t.Fatalf("msg=%q", m.msg)
	}
	press(m, "S", "r", "2")
	if b, _ := os.ReadFile(filepath.Join(dir, "out")); !strings.HasPrefix(string(b), "agent prompt p2 ") {
		t.Fatalf("args %q", b)
	}
}

func TestApprove(t *testing.T) {
	m := reviewModel(t)
	pendingThread(m, t, "+x")
	press(m, "S", "a")
	s := stored(t, m)
	if len(s.Threads) != 0 || len(s.Archived) != 1 || m.msg != "approved; threads archived" {
		t.Fatalf("state %+v msg=%q", s, m.msg)
	}
	press(m, "S", "esc") // anything else cancels
	if m.mode != mNormal {
		t.Fatalf("mode=%d", m.mode)
	}
}

func TestStatusLine(t *testing.T) {
	m := reviewModel(t)
	pendingThread(m, t, "+x")
	m.w, m.d.agents = 400, []Agent{{Status: "idle"}}
	for _, c := range []struct {
		setup func()
		want  string
	}{
		{func() {}, "1 open · 1 pending │ claude: idle"},
		{func() { m.d.agents = nil }, "no Claude pane"},
		{func() { m.d.agents = make([]Agent, 3) }, "3 Claude panes"},
		{func() { m.mode = mSubmit }, "(r)equest changes"},
		{func() { m.mode = mSearch }, "enter save"},
		{func() { m.mode = mMenu }, "j/k move"},
		{func() { m.mode, m.redraft = mGit, func() {} }, "ctrl+r ask Claude again"},
		{func() { m.mode, m.question = mConfirm, "really?" }, "really?"},
		{func() { m.mode, m.msg = mNormal, "hi there" }, "hi there"},
		{func() { m.msg = "" }, "enter comment"},
	} {
		c.setup()
		if got := m.status(); !strings.Contains(got, c.want) {
			t.Errorf("want %q in %q", c.want, got)
		}
	}
}

func TestKeyNavigation(t *testing.T) {
	m := toolModel()
	press(m, "G")
	if m.cur != len(m.rows)-1 {
		t.Fatalf("G: cur=%d", m.cur)
	}
	press(m, "g", "g")
	if m.cur != 0 {
		t.Fatalf("gg: cur=%d", m.cur)
	}
	press(m, "]", "f")
	if m.rows[m.cur].Kind != KFile || m.rows[m.cur].Path != "a" {
		t.Fatalf("]f: %+v", m.rows[m.cur])
	}
	press(m, "]", "f")
	if m.rows[m.cur].Path != "b" {
		t.Fatalf("]f again: %+v", m.rows[m.cur])
	}
	press(m, "[", "f")
	if m.rows[m.cur].Path != "a" {
		t.Fatalf("[f: %+v", m.rows[m.cur])
	}
	// Batched keystrokes replay one by one.
	m.cur = 0
	press(m, "jj")
	if m.cur != 2 {
		t.Fatalf("jj: cur=%d", m.cur)
	}
	press(m, "k")
	press(m, "?")
	if !m.help {
		t.Fatal("? toggles help")
	}
	press(m, "esc")
	if m.help {
		t.Fatal("esc closes help")
	}
	// v on a non-code row does nothing; on a code row toggles visual.
	m.cur = 0
	press(m, "v")
	if m.mode != mNormal {
		t.Fatal("v on header")
	}
	onLine(t, m, "+x")
	press(m, "v")
	if m.mode != mVisual {
		t.Fatal("v selects")
	}
	press(m, "v")
	if m.mode != mNormal {
		t.Fatal("v again leaves visual")
	}
	// Esc leaves a focused file.
	m.focus = "a"
	m.rebuild()
	press(m, "esc")
	if m.focus != "" {
		t.Fatal("esc clears focus")
	}
	// Quit, and quit before anything has loaded.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q quits")
	}
	empty := newModel(&Repo{}, "")
	if _, cmd := empty.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q quits before load")
	}
	if _, cmd := empty.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}); cmd != nil {
		t.Fatal("other keys ignored before load")
	}
}

func TestConfirmAndPrefixKeys(t *testing.T) {
	m := toolModel()
	var got []bool
	m.mode, m.answer = mConfirm, func(y bool) { got = append(got, y) }
	press(m, "y")
	m.mode = mConfirm
	press(m, "n")
	if len(got) != 2 || !got[0] || got[1] || m.mode != mNormal {
		t.Fatalf("answers %v mode=%d", got, m.mode)
	}
}

func TestOutdatedAndResolvedThreadRows(t *testing.T) {
	m := reviewModel(t)
	m.d.state.Threads = []*Thread{
		{ID: "t1", Path: "a", Line: 9, Lines: []string{"+gone"}, Comments: []Comment{{Author: Reviewer, Body: "old"}, {Author: Author, Body: "ok", Pending: true}}},
		{ID: "t2", Resolved: true, Comments: []Comment{{Author: Reviewer, Body: "first line\nsecond"}}},
	}
	m.rebuild()
	var text []string
	for _, r := range m.rows {
		if r.Thread != nil {
			text = append(text, r.Text)
		}
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{"t1 outdated · a:9", "  +gone", "you:", "claude (pending):", "t2 resolved · (whole changeset) · first line"} {
		if !strings.Contains(all, want) {
			t.Errorf("rows lack %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "second") {
		t.Error("resolved Thread shows only the first line")
	}
}
