# A tmux server nothing on the host can see. The editor and the integration
# test binary are built on the host and mounted at /opt/tte, so a code change
# never needs this image rebuilt. Everything runs as root, and tmux-continuum
# counts only the host user's tmux processes, so a server here never stops it
# saving the user's sessions.
FROM alpine:3.22
RUN apk add --no-cache tmux bash ncurses-terminfo-base
ENV LANG=C.UTF-8 TERM=xterm-256color PATH=/opt/tte:$PATH
WORKDIR /root
COPY tmux.conf /root/.tmux.conf
COPY seed-workspace /usr/local/bin/seed-workspace
