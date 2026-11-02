//go:build integration

// Package integration applies a plan to a real tmux server. It runs only
// inside the sandbox container, through `task test:integration`, so no test
// server ever shares a machine with a user's tmux.
package integration

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/datapointchris/tmux-tree-editor/plan"
	"github.com/datapointchris/tmux-tree-editor/tmux"
	"github.com/datapointchris/tmux-tree-editor/tree"
)

func run(t *testing.T, c tmux.Client, args ...string) string {
	t.Helper()
	out, err := c.Run(args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// shape renders a tree as "session: window[panes] window[panes]" lines, sorted.
func shape(tr tree.Tree) string {
	var lines []string
	for _, s := range tr.Sessions {
		var ws []string
		for _, w := range s.Windows {
			var ps []string
			for _, p := range w.Panes {
				ps = append(ps, p.Title)
			}
			ws = append(ws, w.Name+"["+strings.Join(ps, ",")+"]")
		}
		lines = append(lines, s.Name+": "+strings.Join(ws, " "))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestAnEditAppliedToARealServerLeavesTheTreeItDescribes(t *testing.T) {
	if os.Getenv("TMUX_TREE_EDITOR_IN_CONTAINER") != "1" {
		t.Skip("runs only inside the sandbox container: task test:integration")
	}
	c := tmux.Client{Socket: []string{"-S", filepath.Join(t.TempDir(), "s")}}
	t.Cleanup(func() { _, _ = c.Run("kill-server") })

	a := run(t, c, "new-session", "-d", "-s", "infra", "-n", "cluster upgrade", "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}")
	run(t, c, "select-pane", "-t", a, "-T", "A")
	b := run(t, c, "split-window", "-d", "-h", "-t", a, "-P", "-F", "#{pane_id}")
	run(t, c, "select-pane", "-t", b, "-T", "B")
	cp := run(t, c, "new-window", "-d", "-t", "infra:", "-n", "web patch", "-P", "-F", "#{pane_id}")
	run(t, c, "select-pane", "-t", cp, "-T", "C")
	d := run(t, c, "new-session", "-d", "-s", "data", "-n", "pipeline", "-P", "-F", "#{pane_id}")
	run(t, c, "select-pane", "-t", d, "-T", "D")
	e := run(t, c, "new-window", "-d", "-t", "data:", "-n", "backfill", "-P", "-F", "#{pane_id}")
	run(t, c, "select-pane", "-t", e, "-T", "E")
	f := run(t, c, "split-window", "-d", "-h", "-t", e, "-P", "-F", "#{pane_id}")
	run(t, c, "select-pane", "-t", f, "-T", "F")

	base, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	want := base.Clone()
	var infra, data *tree.Session
	for i := range want.Sessions {
		switch want.Sessions[i].Name {
		case "infra":
			infra = &want.Sessions[i]
		case "data":
			data = &want.Sessions[i]
		}
	}
	// web patch moves to data, and B is killed.
	webPatch := infra.Windows[1]
	infra.Windows = infra.Windows[:1]
	infra.Windows[0].Panes = infra.Windows[0].Panes[:1]
	// D, pipeline's only pane, joins cluster upgrade below A, which closes pipeline.
	paneD := data.Windows[0].Panes[0]
	paneD.Stacked = true
	data.Windows[0].Panes = nil
	infra.Windows[0].Panes = append(infra.Windows[0].Panes, paneD)
	// backfill's two panes join web patch after C, which closes backfill.
	webPatch.Panes = append(webPatch.Panes, data.Windows[1].Panes...)
	data.Windows = append(data.Windows[:1], webPatch)
	data.Name = "warehouse"
	// A new session holding a new window with a new pane.
	want.Sessions = append(want.Sessions, tree.Session{ID: "+1", Name: "scratch", Windows: []tree.Window{
		{ID: "+2", Name: "notes", Panes: []tree.Pane{{ID: "+3", Title: "N", Path: "/tmp"}}},
	}})

	steps, err := plan.Diff(base, base, want)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		if s.Shown && strings.HasPrefix(s.Say, "kill window") {
			t.Fatalf("a window emptied by moves closes on its own, but the plan says %q", s.Say)
		}
	}
	if _, err := plan.Apply(c, steps); err != nil {
		t.Fatal(err)
	}
	got, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Join([]string{
		"infra: cluster upgrade[A,D]",
		"scratch: notes[N]",
		"warehouse: web patch[C,E,F]",
	}, "\n")
	if shape(got) != expected {
		t.Fatalf("after apply:\n%s\nwant:\n%s", shape(got), expected)
	}
	layout := run(t, c, "display-message", "-p", "-t", d, "#{pane_top} #{pane_left}")
	if strings.HasPrefix(layout, "0 ") {
		t.Fatalf("D was joined stacked, so it should sit below A, got top/left %q", layout)
	}
}
