# tmux-tree-editor

Arrange a tmux server's sessions, windows and panes in columns, the way a
file manager arranges files. Cut a window and paste it into another session,
move a pane into another window, rename, add and kill. Nothing reaches tmux
until `w` or `q` shows the plan and `y` confirms it.

```text
 sessions   │ windows                    │ panes                   │ preview
 data       │  review bots  3 panes      │  claude  drain the old… │ claude · ~/src/infra
 monitor    │→ cluster upgrade  2 panes  │→ ~/src/infra  below     │ > drain the three…
 infra      │✎ pr 100 review             │                         │
 sites      │  diff tool  2 panes        │                         │
```

A pane is named by its title, or by its directory when the title is only the
host name or the `host:directory` a shell sets. Its program shows only when
it is something other than a shell.

## Keys

| Key | Does |
| --- | --- |
| `j` `k`, `h` `l` | move within a column, move between columns |
| `space` | select or unselect the window or pane under the cursor and move down, as in yazi |
| `x` | cut the window or pane under the cursor, or everything selected |
| `p` | paste after the cursor, in the order cut. A pane pasted in the windows column becomes a window of its own; a window pasted in the panes column moves its panes into that window; windows pasted into a new session replace its empty default window |
| `esc` | clear the cut and the selection |
| `r` | rename a session or window, or retitle a pane |
| `a` | add a session, window or pane after the cursor |
| `d` | mark for killing, or press again to keep; with a selection, every selected one |
| `J` `K` | shift a session, window or pane within its column |
| `u` | undo the last edit |
| `w` | show the plan, then `y` applies it |
| `q` | close; with an edit staged, show the plan, then `y` applies and closes and `d` closes without applying |
| `R` | reload from tmux, discarding the edit |
| `?` | every key |

A marker beside each row says what the plan will do to it: `→` moved, `✎`
renamed, `+` new, `✗` killed, `✂` cut and waiting for a paste, `●` selected.

Sessions are listed in creation order, the order of their ids, which is
`choose-tree`'s default and what id-ordered status-line session lists show.
tmux's own listings sort sessions by name instead.

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

tmux can neither swap nor renumber a session, and a new one is always
created last. So a session that must sit earlier than its id allows, after
`J`, `K` or a new session added mid-list, is rebuilt: a fresh session takes
its attached clients, then its windows, then its name. Once one session is
rebuilt, every session after it is too, because the fresh id is higher than
any it passed. Windows and panes keep running through it. Options and
environment variables set on the old session itself are not carried over.

A pasted pane splits the pane before it along that pane's longer side,
counting a cell as twice as tall as it is wide, as dwindle tiling does in
bspwm and Hyprland. The panes around it are then evened with
`select-layout -E`.

A retitled pane also gets `allow-set-title off`. Shells and agents that set
their own titles would otherwise overwrite the new one at their next prompt.

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
the editor, and `C-a d` leaves and removes the container. Each pane runs a
stand-in under a program's name that prints a screen, so the preview has
something to show.

`task sandbox:mirror` seeds the container from a copy of your own layout
instead: every session, window and pane, with each pane's title, program,
directory, size and screen. Copying runs only `list-panes`, `capture-pane` and
`display-message` against your server. The copy is written under
`~/.cache/tmux-tree-editor/mirror`, and it holds whatever your panes show.

## Development

```bash
task test               # unit tests; none starts tmux
task test:integration   # applies a plan to a real tmux server inside the container
```
