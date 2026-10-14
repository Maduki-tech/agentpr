package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	Reviewer = "reviewer"
	Author   = "author"
)

type Comment struct {
	Author  string    `json:"author"`
	Body    string    `json:"body"`
	Pending bool      `json:"pending,omitempty"`
	At      time.Time `json:"at"`
}

// Thread anchors: Path "" = whole Changeset, Lines empty = whole file,
// otherwise Lines is the raw diff snippet starting at line Line.
type Thread struct {
	ID       string    `json:"id"`
	Path     string    `json:"path,omitempty"`
	Line     int       `json:"line,omitempty"`
	Lines    []string  `json:"lines,omitempty"`
	Resolved bool      `json:"resolved,omitempty"`
	Comments []Comment `json:"comments"`
	Outdated bool      `json:"-"`
}

func (t *Thread) Where() string {
	switch {
	case t.Path == "":
		return "(whole changeset)"
	case len(t.Lines) == 0:
		return t.Path
	case len(t.Lines) == 1:
		return fmt.Sprintf("%s:%d", t.Path, t.Line)
	}
	return fmt.Sprintf("%s:%d-%d", t.Path, t.Line, t.Line+len(t.Lines)-1)
}

func (t *Thread) Pending() bool {
	for _, c := range t.Comments {
		if c.Pending {
			return true
		}
	}
	return false
}

type State struct {
	NextID     int       `json:"next_id"`
	LastReview string    `json:"last_review,omitempty"` // tree snapshot at last submitted Review
	Threads    []*Thread `json:"threads"`
	Archived   []*Thread `json:"archived,omitempty"`
}

func (s *State) Thread(id string) *Thread {
	for _, t := range s.Threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (r *Repo) StatePath() string {
	return filepath.Join(r.GitDir, "agentpr", strings.ReplaceAll(r.Branch, "/", "__")+".json")
}

func loadState(path string) (*State, []byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	s := &State{}
	return s, b, json.Unmarshal(b, s)
}

// updateState is read-modify-write so the TUI never clobbers replies the Author wrote meanwhile.
// ponytail: no file lock, a reply landing in the same millisecond can be lost; add flock if it happens.
func updateState(path string, f func(*State) error) (*State, error) {
	s, _, err := loadState(path)
	if err != nil {
		return nil, err
	}
	if err := f(s); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return nil, err
	}
	return s, os.Rename(tmp, path)
}
