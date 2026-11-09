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

## Never write the breaking-change trailer in a commit message

The words `BREAKING CHANGE` — either number, colon or not, subject or body — cut a major release
here, and a major on this repo is an outage rather than a version. `commit-analyzer-cz` matches
them unanchored against the raw message and ORs the result with the configured major rules, so
`.semrelrc` cannot stop it and it majors even a `fix:` commit.

The module path carries no `/vN` suffix, so once a major exists `go install …@latest` cannot see it
and silently resolves the highest v1 instead. An installer comparing versions then reports the tool
stale forever while installing nothing.

**The ban covers a commit that merely discusses the trailer.** Name it some other way — "that
marker" — and never quote it.

Deliberate majors use `chore(release-major)`, the one subject `.semrelrc` leaves as a major. The
release carrying one also adds the `/vN` suffix to the module path, so
`go install github.com/datapointchris/tmux-tree-editor/vN@latest` resolves the tag.

After an accidental major, delete the vN releases and their tags, then tag a baseline on `main`
above the offending commit, or the next push cuts the major again. `go list -m
github.com/datapointchris/tmux-tree-editor@latest` answering the v1 tag confirms it.

## Where things are

- `tree/` is the model, keyed by tmux's own ids. An item the editor will
  create carries a `+N` placeholder id.
- `plan/` turns base, live and want trees into ordered tmux steps and runs
  them. Its package comment holds the ordering and placeholder rules.
- `ui/` is the Bubble Tea model. One action table builds the key dispatch,
  the footer and the `?` overlay.
- `tmux/` is the only code that runs the tmux binary. Given a socket, it
  strips `$TMUX` and `$TMUX_PANE` from the child's environment.
