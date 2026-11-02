package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/datapointchris/tmux-tree-editor/tree"
)

type fixedServer struct{ t tree.Tree }

func (f fixedServer) Read() (tree.Tree, error)      { return f.t.Clone(), nil }
func (f fixedServer) Run(...string) (string, error) { return "", nil }
func (f fixedServer) Capture(string, int) string    { return "" }

func press(m *Model, keys ...string) {
	for _, k := range keys {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		m.Update(msg)
	}
}

func TestCuttingAWindowAndPastingItOnAnotherSessionStagesOneMove(t *testing.T) {
	server := fixedServer{tree.Tree{Sessions: []tree.Session{
		{ID: "$1", Name: "data", Windows: []tree.Window{{ID: "@1", Name: "pipeline", Panes: []tree.Pane{{ID: "%1"}}}}},
		{ID: "$2", Name: "infra", Windows: []tree.Window{
			{ID: "@2", Name: "cluster upgrade", Panes: []tree.Pane{{ID: "%2"}}},
			{ID: "@3", Name: "web patch", Panes: []tree.Pane{{ID: "%3"}}},
		}},
	}}}
	m, err := New(server, "")
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	press(m, "j", "l", "j", "x", "h", "k", "p")

	if m.staged != 1 {
		t.Fatalf("want one staged move, got %d", m.staged)
	}
	got := m.want.Sessions[0].Windows
	if len(got) != 2 || got[1].ID != "@3" {
		t.Fatalf("want web patch appended to data, got %+v", got)
	}
	press(m, "u")
	if m.staged != 0 {
		t.Fatalf("undo should leave nothing staged, got %d", m.staged)
	}
}
