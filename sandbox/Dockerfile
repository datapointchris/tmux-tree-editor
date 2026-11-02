# A tmux server nothing on the host can see. The editor and the integration
# test binary are built on the host and mounted at /opt/tte, so a code change
# never needs this image rebuilt. Everything runs as root, and tmux-continuum
# counts only the host user's tmux processes, so a server here never stops it
# saving the user's sessions.
FROM alpine:3.22
RUN apk add --no-cache tmux bash ncurses-terminfo-base
ENV LANG=C.UTF-8 TERM=xterm-256color PATH=/opt/tte:$PATH
WORKDIR /root
# The config is the system one, so a mirror that sets $HOME to the host's
# home still loads it.
COPY tmux.conf /etc/tmux.conf
COPY seed-workspace fake-pane /usr/local/bin/
COPY workspace /usr/local/share/tmux-tree-editor/workspace
