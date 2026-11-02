package plan

import (
	"strings"
	"testing"

	"github.com/datapointchris/tmux-tree-editor/tree"
)

func pane(id string) tree.Pane { return tree.Pane{ID: id, Command: "zsh"} }

func workspace() tree.Tree {
	return tree.Tree{Sessions: []tree.Session{
		{ID: "$1", Name: "infra", Windows: []tree.Window{
			{ID: "@1", Name: "cluster upgrade", Panes: []tree.Pane{pane("%1"), pane("%2")}},
			{ID: "@2", Name: "web patch", Panes: []tree.Pane{pane("%3")}},
		}},
		{ID: "$2", Name: "data", Windows: []tree.Window{
			{ID: "@3", Name: "pipeline", Panes: []tree.Pane{pane("%4")}},
		}},
	}}
}

func commands(steps []Step) []string {
	var out []string
	for _, s := range steps {
		if s.Do == nil {
			out = append(out, strings.Join(s.Args, " "))
		}
	}
	return out
}

func TestAnEditPutBackStagesNothing(t *testing.T) {
	base := workspace()
	steps, err := Diff(base, base, base.Clone())
	if err != nil {
		t.Fatal(err)
	}
	if n := Shown(steps); n != 0 {
		t.Fatalf("staged %d steps for an unchanged tree: %v", n, commands(steps))
	}
}

func TestAWindowMovedToAnotherSessionIsOneMove(t *testing.T) {
	base := workspace()
	want := base.Clone()
	moved := want.Sessions[0].Windows[1]
	want.Sessions[0].Windows = want.Sessions[0].Windows[:1]
	want.Sessions[1].Windows = append(want.Sessions[1].Windows, moved)

	steps, err := Diff(base, base, want)
	if err != nil {
		t.Fatal(err)
	}
	got := commands(steps)
	if Shown(steps) != 1 || len(got) != 1 || got[0] != "move-window -d -s @2 -t $2:" {
		t.Fatalf("want one move-window, got %v", got)
	}
}

func TestAWindowLosingItsLastPaneIsHeldOpenUntilTheMoveIsDone(t *testing.T) {
	base := workspace()
	want := base.Clone()
	want.Sessions[1].Windows[0].Panes = nil
	want.Sessions[0].Windows[0].Panes = append(want.Sessions[0].Windows[0].Panes, pane("%4"))

	steps, err := Diff(base, base, want)
	if err != nil {
		t.Fatal(err)
	}
	got := commands(steps)
	hold, join := -1, -1
	for i, c := range got {
		switch {
		case strings.HasPrefix(c, "split-window -d -t @3"):
			hold = i
		case strings.HasPrefix(c, "join-pane -d -h -s %4 -t %2"):
			join = i
		}
	}
	if hold < 0 || join < hold {
		t.Fatalf("want a placeholder in @3 before the join, got %v", got)
	}
	if !strings.HasPrefix(got[len(got)-1], "kill-pane -t {~") {
		t.Fatalf("want the placeholder removed last, got %v", got)
	}
}

func TestAPaneGoneSinceLoadRefuses(t *testing.T) {
	base := workspace()
	live := base.Clone()
	live.Sessions[0].Windows[0].Panes = live.Sessions[0].Windows[0].Panes[:1]

	if _, err := Diff(base, live, base.Clone()); err == nil || !strings.Contains(err.Error(), "%2 is gone") {
		t.Fatalf("want a refusal naming %%2, got %v", err)
	}
}

func TestAPaneOpenedElsewhereIsNeverKilled(t *testing.T) {
	base := workspace()
	live := base.Clone()
	live.Sessions[0].Windows[1].Panes = append(live.Sessions[0].Windows[1].Panes, pane("%9"))
	want := base.Clone()
	want.Sessions[0].Windows = want.Sessions[0].Windows[:1]

	steps, err := Diff(base, live, want)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range commands(steps) {
		if strings.Contains(c, "kill-window") || strings.Contains(c, "%9") {
			t.Fatalf("the window holding %%9 must survive, got %v", commands(steps))
		}
	}
	if got := commands(steps); len(got) != 1 || got[0] != "kill-pane -t %3" {
		t.Fatalf("want only the loaded pane killed, got %v", got)
	}
}
