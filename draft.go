package main

// Commit messages and PR texts drafted by Claude Code in headless mode, prefilled into the input box.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	commitPrompt = "Write a git commit message for the change on stdin (a diff stat, then the diff). " +
		"Conventional Commits style: a subject line of at most 72 characters, then optionally a blank line and a short body saying why. " +
		"Output only the message: no code fences, no preamble."
	prPrompt = "Write a pull request for the change on stdin (a diff stat, then the diff). " +
		"First line: the title, at most 72 characters. Then a blank line and a concise summary of the changes as the body. " +
		"Output only the title and body: no code fences, no preamble, no labels like \"Title:\"."
	maxDiff = 100 << 10
)

// runClaude runs `claude -p` in dir with input on stdin; tests replace it.
var runClaude = func(dir, prompt, input string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "--model", "haiku", prompt)
	cmd.Dir = dir
	// The run's Stop hook is `agentpr hook`; it must not open another agentpr split.
	cmd.Env = append(os.Environ(), "AGENTPR_AUTO_OPEN=0")
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	var ee *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return "", errors.New("claude not found: install Claude Code to draft messages")
	case ctx.Err() != nil:
		return "", errors.New("claude took over 60s to draft a message")
	case errors.As(err, &ee):
		return "", fmt.Errorf("claude -p: %s", strings.TrimSpace(string(ee.Stderr)))
	case err != nil:
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// diffText is the stat and the diff for git diff args, cut to maxDiff with a note saying so.
func (r Repo) diffText(args ...string) (string, error) {
	stat, err := git(r.Root, nil, append([]string{"diff", "--stat"}, args...)...)
	if err != nil {
		return "", err
	}
	diff, err := git(r.Root, nil, append([]string{"diff", "--no-color", "--no-ext-diff"}, args...)...)
	if err != nil {
		return "", err
	}
	if len(diff) > maxDiff {
		diff = diff[:strings.LastIndexByte(diff[:maxDiff], '\n')+1] + "[diff truncated at 100KB; the stat above lists every file]"
	}
	return stat + "\n\n" + diff, nil
}

type drafted struct {
	gen         int
	placeholder string
	text        string
	err         error
}

// draft opens the git input like ask, then fills it with what Claude writes about input()'s
// diff for prompt; ctrl+r asks again. Without claude the box just stays empty.
func (m *model) draft(placeholder, prompt string, input func() (string, error), f func(string)) {
	m.ask(mGit, placeholder, f)
	gen, dir := m.gen, m.repo.Root
	m.redraft = func() {
		m.input.Reset()
		m.input.Placeholder = "Claude is drafting the " + placeholder + "… (or type your own)"
		m.cmd = func() tea.Msg {
			in, err := input()
			text := ""
			if err == nil {
				text, err = runClaude(dir, prompt, in)
			}
			return drafted{gen, placeholder, unfence(text), err}
		}
	}
	m.redraft()
}

// gotDraft prefills the input box the draft was made for, unless it's gone or already typed in.
func (m *model) gotDraft(d drafted) {
	if d.gen != m.gen || m.mode != mGit {
		return
	}
	m.input.Placeholder = d.placeholder
	if d.err != nil {
		m.msg = d.err.Error()
	} else if m.input.Value() == "" {
		m.input.SetValue(d.text)
	}
}

// unfence drops a ``` wrapper the model may add despite the prompt.
func unfence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	_, s, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
