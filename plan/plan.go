// Package plan turns an edited tree into the tmux commands that make the
// server match it, and runs them.
//
// Three trees go in. base is what the editor loaded, live is a fresh read
// taken at apply time, and want is the edit. An item is killed only when it
// was in base and is missing from want, so anything created on the server
// while the editor was open is left alone. Every existing id in want must
// still be live, or the plan refuses rather than guessing.
//
// Steps run in a fixed order: renames, then creations, then moves, then
// reordering, then kills, then removal of the placeholders. tmux destroys a
// window with its last pane and a session with its last window, so a window
// or session about to lose everything it holds gets a placeholder first and
// loses it last. That also keeps break-pane off a window's last pane, where
// tmux would move the whole window rather than one pane out of it.
package plan

import (
	"fmt"
	"strings"

	"github.com/datapointchris/tmux-tree-editor/tree"
)

type Runner interface {
	Run(args ...string) (string, error)
}

type Step struct {
	// Say is what the plan screen shows, and what an error names.
	Say string
	// Args are tmux arguments. A "{+N}" or "{~N}" token, optionally followed by
	// ":" or ".", is a reference resolved before the step runs.
	Args []string
	// Bind names the references this step creates, one per field tmux prints
	// for its -P -F format.
	Bind []string
	// Guard is a target that must still exist, or the step is skipped.
	Guard string
	// Shown is false for mechanics nobody asked for, such as placeholders.
	Shown bool
	// Do is a step that reads tmux while it runs, such as a reorder.
	Do func(r Runner, resolve func(string) string) error
}

const placeholderName = "tmux-tree-editor-placeholder"

// ref wraps a tree id for use in Args: a placeholder id becomes a reference.
func ref(id string) string {
	if tree.IsNew(id) {
		return "{" + id + "}"
	}
	return id
}

// Shown counts the steps the user asked for.
func Shown(steps []Step) int {
	n := 0
	for _, s := range steps {
		if s.Shown {
			n++
		}
	}
	return n
}

type differ struct {
	base, live, want tree.Tree
	lb, ll, lw       tree.Locations
	steps, cleanup   []Step
	holders          int
}

func Diff(base, live, want tree.Tree) ([]Step, error) {
	want = want.Clone()
	want.Prune()
	d := &differ{base: base, live: live, want: want, lb: base.Locate(), ll: live.Locate(), lw: want.Locate()}
	if err := d.checkLive(); err != nil {
		return nil, err
	}
	d.renames()
	d.newSessions()
	d.holdSessions()
	d.holdWindows()
	d.windows()
	d.panes()
	d.order()
	d.kills()
	return append(d.steps, d.cleanup...), nil
}

func (d *differ) add(s Step) { d.steps = append(d.steps, s) }

func (d *differ) holder() string {
	d.holders++
	return fmt.Sprintf("{~%d}", d.holders)
}

func (d *differ) liveWindow(loc [2]int) tree.Window { return d.live.Sessions[loc[0]].Windows[loc[1]] }

func (d *differ) livePane(loc [3]int) tree.Pane {
	return d.live.Sessions[loc[0]].Windows[loc[1]].Panes[loc[2]]
}

func (d *differ) checkLive() error {
	for _, s := range d.want.Sessions {
		if _, ok := d.ll.Session[s.ID]; !ok && !tree.IsNew(s.ID) {
			return fmt.Errorf("session %q is gone since the editor loaded; press R to reload", s.Name)
		}
		for _, w := range s.Windows {
			if _, ok := d.ll.Window[w.ID]; !ok && !tree.IsNew(w.ID) {
				return fmt.Errorf("window %q is gone since the editor loaded; press R to reload", w.Name)
			}
			for _, p := range w.Panes {
				if _, ok := d.ll.Pane[p.ID]; !ok && !tree.IsNew(p.ID) {
					return fmt.Errorf("pane %s is gone since the editor loaded; press R to reload", p.ID)
				}
			}
		}
	}
	return nil
}

func (d *differ) renames() {
	for _, s := range d.want.Sessions {
		if tree.IsNew(s.ID) {
			continue
		}
		if was := d.live.Sessions[d.ll.Session[s.ID]].Name; s.Name != was {
			d.add(Step{Say: fmt.Sprintf("rename session %q to %q", was, s.Name), Args: []string{"rename-session", "-t", s.ID, s.Name}, Shown: true})
		}
		for _, w := range s.Windows {
			if tree.IsNew(w.ID) {
				continue
			}
			was := d.liveWindow(d.ll.Window[w.ID]).Name
			switch w.Name {
			case was:
			case "":
				d.add(Step{Say: fmt.Sprintf("let window %q name itself", was), Args: []string{"set-window-option", "-t", w.ID, "automatic-rename", "on"}, Shown: true})
			default:
				d.add(Step{Say: fmt.Sprintf("rename window %q to %q", was, w.Name), Args: []string{"rename-window", "-t", w.ID, w.Name}, Shown: true})
			}
			for _, p := range w.Panes {
				if tree.IsNew(p.ID) {
					continue
				}
				if was := d.livePane(d.ll.Pane[p.ID]).Title; p.Title != was {
					d.add(Step{Say: fmt.Sprintf("retitle pane %s to %q", p.ID, p.Title), Args: []string{"select-pane", "-t", p.ID, "-T", p.Title}, Shown: true})
				}
			}
		}
	}
}

func firstPath(s tree.Session) string {
	for _, w := range s.Windows {
		for _, p := range w.Panes {
			if p.Path != "" {
				return p.Path
			}
		}
	}
	return ""
}

func withPath(args []string, path string) []string {
	if path == "" {
		return args
	}
	return append(args, "-c", path)
}

func (d *differ) newSessions() {
	for _, s := range d.want.Sessions {
		if !tree.IsNew(s.ID) {
			continue
		}
		h := d.holder()
		args := withPath([]string{"new-session", "-d", "-s", s.Name, "-n", placeholderName}, firstPath(s))
		d.add(Step{Say: fmt.Sprintf("create session %q", s.Name), Args: append(args, "-P", "-F", "#{session_id} #{window_id}"), Bind: []string{ref(s.ID), h}, Shown: true})
		d.cleanup = append(d.cleanup, Step{Say: "remove a placeholder window", Args: []string{"kill-window", "-t", h}, Guard: h})
	}
}

// holdSessions gives a placeholder window to a kept session whose own windows
// all move to other sessions, so it survives until windows arrive.
func (d *differ) holdSessions() {
	for _, s := range d.want.Sessions {
		if tree.IsNew(s.ID) {
			continue
		}
		leaving, staying := 0, 0
		for _, w := range d.live.Sessions[d.ll.Session[s.ID]].Windows {
			if loc, ok := d.lw.Window[w.ID]; ok && d.want.Sessions[loc[0]].ID != s.ID {
				leaving++
			} else {
				staying++
			}
		}
		if leaving > 0 && staying == 0 {
			h := d.holder()
			d.add(Step{Say: fmt.Sprintf("hold session %q open", s.Name), Args: []string{"new-window", "-d", "-t", s.ID + ":", "-n", placeholderName, "-P", "-F", "#{window_id}"}, Bind: []string{h}})
			d.cleanup = append(d.cleanup, Step{Say: "remove a placeholder window", Args: []string{"kill-window", "-t", h}, Guard: h})
		}
	}
}

// holdWindows gives a placeholder pane to any window whose panes all move out.
// A pane that will be killed still counts as staying, because kills run after
// every move.
func (d *differ) holdWindows() {
	for _, s := range d.live.Sessions {
		for _, w := range s.Windows {
			leaving, staying := 0, 0
			for _, p := range w.Panes {
				if loc, ok := d.lw.Pane[p.ID]; ok && d.want.Sessions[loc[0]].Windows[loc[1]].ID != w.ID {
					leaving++
				} else {
					staying++
				}
			}
			if leaving > 0 && staying == 0 {
				h := d.holder()
				d.add(Step{Say: fmt.Sprintf("hold window %q open", w.Name), Args: []string{"split-window", "-d", "-t", w.ID, "-P", "-F", "#{pane_id}"}, Bind: []string{h}})
				d.cleanup = append(d.cleanup, Step{Say: "remove a placeholder pane", Args: []string{"kill-pane", "-t", h}, Guard: h})
			}
		}
	}
}

func (d *differ) windows() {
	for _, s := range d.want.Sessions {
		target := ref(s.ID) + ":"
		for _, w := range s.Windows {
			switch {
			case tree.IsNew(w.ID):
				first := w.Panes[0]
				var args []string
				if tree.IsNew(first.ID) {
					args = []string{"new-window", "-d", "-t", target}
				} else {
					args = []string{"break-pane", "-d", "-s", first.ID, "-t", target}
				}
				if w.Name != "" {
					args = append(args, "-n", w.Name)
				}
				if tree.IsNew(first.ID) {
					args = withPath(args, first.Path)
					d.add(Step{Say: fmt.Sprintf("create window %q in session %q", w.Name, s.Name), Args: append(args, "-P", "-F", "#{window_id} #{pane_id}"), Bind: []string{ref(w.ID), ref(first.ID)}, Shown: true})
				} else {
					d.add(Step{Say: fmt.Sprintf("move pane %q (%s) into a new window in session %q", first.Title, first.Command, s.Name), Args: append(args, "-P", "-F", "#{window_id}"), Bind: []string{ref(w.ID)}, Shown: true})
				}
			case d.live.Sessions[d.ll.Window[w.ID][0]].ID != s.ID:
				d.add(Step{Say: fmt.Sprintf("move window %q to session %q", w.Name, s.Name), Args: []string{"move-window", "-d", "-s", w.ID, "-t", target}, Shown: true})
			}
		}
	}
}

func (d *differ) panes() {
	for _, s := range d.want.Sessions {
		for _, w := range s.Windows {
			for i, p := range w.Panes {
				if tree.IsNew(w.ID) && i == 0 {
					if tree.IsNew(p.ID) && p.Title != "" {
						d.add(Step{Say: "title the new pane", Args: []string{"select-pane", "-t", ref(p.ID), "-T", p.Title}})
					}
					continue
				}
				anchor := ref(w.ID)
				if i > 0 {
					anchor = ref(w.Panes[i-1].ID)
				}
				split := "-h"
				if p.Stacked {
					split = "-v"
				}
				switch {
				case tree.IsNew(p.ID):
					args := withPath([]string{"split-window", "-d", split, "-t", anchor}, p.Path)
					d.add(Step{Say: fmt.Sprintf("create a pane in window %q", w.Name), Args: append(args, "-P", "-F", "#{pane_id}"), Bind: []string{ref(p.ID)}, Shown: true})
					if p.Title != "" {
						d.add(Step{Say: "title the new pane", Args: []string{"select-pane", "-t", ref(p.ID), "-T", p.Title}})
					}
				case d.windowOfLivePane(p.ID) != w.ID:
					d.add(Step{Say: fmt.Sprintf("move pane %q (%s) into window %q", p.Title, p.Command, w.Name), Args: []string{"join-pane", "-d", split, "-s", p.ID, "-t", anchor}, Shown: true})
				default:
					continue
				}
				d.add(Step{Say: "even the panes around it", Args: []string{"select-layout", "-E", "-t", ref(p.ID)}})
			}
		}
	}
}

func (d *differ) windowOfLivePane(id string) string {
	loc := d.ll.Pane[id]
	return d.live.Sessions[loc[0]].Windows[loc[1]].ID
}

func (d *differ) order() {
	for _, s := range d.want.Sessions {
		ids := make([]string, len(s.Windows))
		for i, w := range s.Windows {
			ids[i] = ref(w.ID)
		}
		say := ""
		if !tree.IsNew(s.ID) && d.reordered(ids, windowIDs(d.live.Sessions[d.ll.Session[s.ID]].Windows)) {
			say = fmt.Sprintf("reorder the windows of session %q", s.Name)
		}
		d.add(Step{Say: say, Shown: say != "", Do: reorder("list-windows", "#{window_id}", "swap-window", ref(s.ID), ids)})
		for _, w := range s.Windows {
			pids := make([]string, len(w.Panes))
			for i, p := range w.Panes {
				pids[i] = ref(p.ID)
			}
			say := ""
			if !tree.IsNew(w.ID) && d.reordered(pids, paneIDs(d.liveWindow(d.ll.Window[w.ID]).Panes)) {
				say = fmt.Sprintf("reorder the panes of window %q", w.Name)
			}
			d.add(Step{Say: say, Shown: say != "", Do: reorder("list-panes", "#{pane_id}", "swap-pane", ref(w.ID), pids)})
		}
	}
}

// reordered reports whether the ids that were already live here appear in a
// different relative order in want.
func (d *differ) reordered(want, live []string) bool {
	present := map[string]bool{}
	for _, id := range live {
		present[id] = true
	}
	var kept []string
	for _, id := range want {
		if present[id] {
			kept = append(kept, id)
		}
	}
	keptSet := map[string]bool{}
	for _, id := range kept {
		keptSet[id] = true
	}
	var was []string
	for _, id := range live {
		if keptSet[id] {
			was = append(was, id)
		}
	}
	return strings.Join(kept, " ") != strings.Join(was, " ")
}

func windowIDs(ws []tree.Window) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}
	return out
}

func paneIDs(ps []tree.Pane) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

// reorder puts the items it manages into the wanted order inside the slots
// they already occupy, swapping by id. Items it does not manage, such as a
// placeholder or a window opened elsewhere, keep their slots.
func reorder(list, format, swap, parent string, want []string) func(Runner, func(string) string) error {
	return func(r Runner, resolve func(string) string) error {
		out, err := r.Run(list, "-t", resolve(parent), "-F", format)
		if err != nil {
			return err
		}
		current := strings.Split(out, "\n")
		managed := map[string]bool{}
		var desired []string
		for _, id := range want {
			id = resolve(id)
			managed[id] = true
			desired = append(desired, id)
		}
		var slots []int
		for i, id := range current {
			if managed[id] {
				slots = append(slots, i)
			}
		}
		var order []string
		for _, id := range desired {
			for _, c := range current {
				if c == id {
					order = append(order, id)
					break
				}
			}
		}
		for k, id := range order {
			at := current[slots[k]]
			if at == id {
				continue
			}
			if _, err := r.Run(swap, "-d", "-s", id, "-t", at); err != nil {
				return err
			}
			for i, c := range current {
				if c == id {
					current[i] = at
				}
			}
			current[slots[k]] = id
		}
		return nil
	}
}

func (d *differ) kills() {
	killedSession := map[string]bool{}
	killedWindow := map[string]bool{}
	for _, s := range d.base.Sessions {
		loc, live := d.ll.Session[s.ID]
		if _, kept := d.lw.Session[s.ID]; kept || !live {
			continue
		}
		whole := true
		for _, w := range d.live.Sessions[loc].Windows {
			_, inBase := d.lb.Window[w.ID]
			_, moved := d.lw.Window[w.ID]
			if !inBase || moved || !d.allPanesFromBase(w) {
				whole = false
			}
		}
		if whole {
			killedSession[s.ID] = true
			d.add(Step{Say: fmt.Sprintf("kill session %q", s.Name), Args: []string{"kill-session", "-t", s.ID}, Guard: s.ID, Shown: true})
		}
	}
	for _, s := range d.base.Sessions {
		for _, w := range s.Windows {
			loc, live := d.ll.Window[w.ID]
			if _, kept := d.lw.Window[w.ID]; kept || !live || killedSession[d.live.Sessions[loc[0]].ID] {
				continue
			}
			if d.allPanesFromBase(d.liveWindow(loc)) {
				killedWindow[w.ID] = true
				d.add(Step{Say: fmt.Sprintf("kill window %q", w.Name), Args: []string{"kill-window", "-t", w.ID}, Guard: w.ID, Shown: true})
			}
		}
	}
	for _, s := range d.base.Sessions {
		for _, w := range s.Windows {
			for _, p := range w.Panes {
				loc, live := d.ll.Pane[p.ID]
				if _, kept := d.lw.Pane[p.ID]; kept || !live {
					continue
				}
				owner := d.live.Sessions[loc[0]]
				if killedSession[owner.ID] || killedWindow[owner.Windows[loc[1]].ID] {
					continue
				}
				d.add(Step{Say: fmt.Sprintf("kill pane %s (%s)", p.ID, p.Command), Args: []string{"kill-pane", "-t", p.ID}, Guard: p.ID, Shown: true})
			}
		}
	}
}

// allPanesFromBase reports whether killing the whole window would kill only
// panes the editor loaded, never one opened elsewhere since.
func (d *differ) allPanesFromBase(w tree.Window) bool {
	for _, p := range w.Panes {
		if _, ok := d.lb.Pane[p.ID]; !ok {
			return false
		}
	}
	return true
}

// present reports whether a session, window or pane id is on the server.
// display-message cannot answer this: it resolves its target with
// CMD_FIND_CANFAIL, so a missing pane falls back to another rather than failing.
func present(r Runner, id string) bool {
	out, err := r.Run("list-panes", "-a", "-F", "#{session_id} #{window_id} #{pane_id}")
	if err != nil {
		return false
	}
	for _, field := range strings.Fields(out) {
		if field == id {
			return true
		}
	}
	return false
}

// Apply runs the steps in order and stops at the first failure, returning how
// many ran.
func Apply(r Runner, steps []Step) (int, error) {
	refs := map[string]string{}
	resolve := func(arg string) string {
		if !strings.HasPrefix(arg, "{") {
			return arg
		}
		end := strings.Index(arg, "}")
		if end < 0 {
			return arg
		}
		if real, ok := refs[arg[:end+1]]; ok {
			return real + arg[end+1:]
		}
		return arg
	}
	for i, st := range steps {
		if st.Guard != "" && !present(r, resolve(st.Guard)) {
			continue
		}
		if st.Do != nil {
			if err := st.Do(r, resolve); err != nil {
				return i, fmt.Errorf("%s: %w", st.Say, err)
			}
			continue
		}
		args := make([]string, len(st.Args))
		for k, a := range st.Args {
			args[k] = resolve(a)
		}
		out, err := r.Run(args...)
		if err != nil {
			return i, fmt.Errorf("%s: %w", st.Say, err)
		}
		if len(st.Bind) > 0 {
			fields := strings.Fields(out)
			if len(fields) < len(st.Bind) {
				return i, fmt.Errorf("%s: tmux printed %q, expected %d ids", st.Say, out, len(st.Bind))
			}
			for k, name := range st.Bind {
				refs[name] = fields[k]
			}
		}
	}
	return len(steps), nil
}
