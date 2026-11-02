# tmux-tree-editor

Arrange a tmux server's sessions, windows and panes in columns, the way a
file manager arranges files. Cut a window and paste it into another session,
move a pane into another window, rename, add and kill. Nothing reaches tmux
until `w` shows the plan and `y` confirms it.

```text
 sessions   │ windows · infra            │ panes · cluster upgrade │ preview
 data       │  review bots  3            │  claude  drain the old… │ ...
 monitor    │→ cluster upgrade  2        │  zsh     ~/src/infra    │
 infra      │✎ pr 100 review  1          │                         │
 sites      │  diff tool  2              │                         │
```

## Keys

| Key | Does |
| --- | --- |
| `j` `k`, `h` `l` | move within a column, move between columns |
| `x` | cut the window or pane under the cursor |
| `p` | paste after the cursor. A pane goes beside its neighbor; pasted in the windows column it becomes a window of its own |
| `P` | paste a pane below its neighbor |
| `r` | rename a session or window, or retitle a pane |
| `a` | add a session, window or pane after the cursor |
| `d` | mark for killing, or press again to keep |
| `J` `K` | shift a window or pane within its column |
| `u` | undo the last edit |
| `w` | show the plan, then `y` applies it |
| `R` | reload from tmux, discarding the edit |
| `?` | every key |

A marker beside each row says what the plan will do to it: `→` moved, `✎`
renamed, `+` new, `✗` killed, `✂` cut and waiting for a paste.

## How an edit is applied

The plan compares three trees: the one the editor loaded, a fresh read of
tmux taken when `w` is pressed, and the edit. It refuses when something the
edit names has gone since loading. It kills only what was loaded and then
removed, so a window opened elsewhere while the editor was up survives.

Commands run in a fixed order: renames, creations, moves, reordering, kills.
tmux closes a window with its last pane and a session with its last window,
so a window or session about to lose everything gets a placeholder first and
loses it last. That also keeps `break-pane` off a window's last pane, where
tmux would move the whole window instead of one pane.

A joined pane splits its neighbor, and the panes around it are evened with
`select-layout -E`.

## Install and run

```bash
go install github.com/datapointchris/tmux-tree-editor@latest
tmux-tree-editor               # the server this pane belongs to
tmux-tree-editor -L scratch    # the server `tmux -L scratch` started
```

A popup binding in `tmux.conf`:

```tmux
bind e display-popup -E -w 94% -h 90% tmux-tree-editor
```

## Trying it without touching your tmux

`task sandbox` starts a container holding its own tmux server, seeds a
workspace of sessions, windows and titled panes, and attaches with the editor
open. Its prefix is `C-a`, so it works inside your own tmux. `C-a e` reopens
the editor, and `C-a d` leaves and removes the container.

## Development

```bash
task test               # unit tests; none starts tmux
task test:integration   # applies a plan to a real tmux server inside the container
```
