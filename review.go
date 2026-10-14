package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

func buildPrompt(threads []*Thread) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Code review from agentpr: REQUEST CHANGES (%d threads)\n\n", len(threads))
	b.WriteString("For each thread: change the code if you agree, then answer the thread by running\n")
	b.WriteString("  agentpr reply <thread-id> \"<what you changed, or why you disagree>\"\n")
	b.WriteString("from the repository. Do not resolve threads; the reviewer does that.\n")
	for _, t := range threads {
		fmt.Fprintf(&b, "\n## %s · %s\n", t.ID, t.Where())
		if len(t.Lines) > 0 {
			b.WriteString("```diff\n" + strings.Join(t.Lines, "\n") + "\n```\n")
		}
		for _, c := range t.Comments {
			who := "reviewer"
			if c.Author == Author {
				who = "you"
			}
			fmt.Fprintf(&b, "%s: %s\n", who, strings.ReplaceAll(c.Body, "\n", "\n  "))
		}
	}
	return b.String()
}

type Agent struct {
	Pane   string `json:"pane_id"`
	Agent  string `json:"agent"`
	Status string `json:"agent_status"`
	Cwd    string `json:"cwd"`
	Title  string `json:"terminal_title_stripped"`
}

// agents lists Claude panes in herdr working inside root.
func agents(root string) ([]Agent, error) {
	out, err := exec.Command("herdr", "agent", "list").Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var resp struct {
		Result struct {
			Agents []Agent `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, err
	}
	var res []Agent
	for _, a := range resp.Result.Agents {
		if a.Agent == "claude" && (a.Cwd == root || strings.HasPrefix(a.Cwd, root+"/")) {
			res = append(res, a)
		}
	}
	return res, nil
}

func sendPrompt(pane, text string) error {
	out, err := exec.Command("herdr", "agent", "prompt", pane, text).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func copyClipboard(text string) error {
	cmd := exec.Command("wl-copy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = exec.Command("xclip", "-selection", "clipboard")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
