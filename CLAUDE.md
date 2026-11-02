# tmux-tree-editor — Claude Code instructions

## Never start tmux on the host

Every test or manual run that starts a tmux server does it inside the sandbox
container. A test server on the host can land a session in the user's live
server, be snapshotted by tmux-resurrect, or survive the run and stop
tmux-continuum saving the user's sessions, since continuum counts the user's
tmux processes. The container runs as root and ends every process in it when
its first process exits, so none of that can happen there.

- `task test` never starts tmux. Keep it that way: a test that needs a server
  goes in `integration/`, behind the `integration` build tag.
- `task test:integration` builds the test binary on the host and runs it in
  the container.
- To drive the TUI, run a detached container, seed it with
  `seed-workspace --detached`, start the editor in a session there, then
  `docker exec <name> tmux send-keys` and `capture-pane -p`. `capture-pane
  -p` drops styling, so the cursor highlight does not show in it.
- Never run the built binary against the default server from a session here.
  Without `-L` or `-S` it edits the server it runs inside, which is the
  user's.

## Where things are

- `tree/` is the model, keyed by tmux's own ids. An item the editor will
  create carries a `+N` placeholder id.
- `plan/` turns base, live and want trees into ordered tmux steps and runs
  them. Its package comment holds the ordering and placeholder rules.
- `ui/` is the Bubble Tea model. One action table builds the key dispatch,
  the footer and the `?` overlay.
- `tmux/` is the only code that runs the tmux binary. Given a socket, it
  strips `$TMUX` and `$TMUX_PANE` from the child's environment.
