FROM alpine:3.21
# docker-cli + docker-cli-compose are needed here because the in-app
# self-updater (internal/api/update.go) runs `docker compose up -d --build`
# from *inside* this container against the mounted host docker.sock — see
# docker-compose.yml's `/var/run/docker.sock` and `..:/opt/pubobs` mounts.
# That's the whole point of the feature (an admin-triggered one-click
# rebuild/restart), but it does mean this container needs host-docker-equivalent
# privilege: we deliberately run as root (no `adduser`/`USER` drop here, unlike
# earlier revisions of this image) because a non-root user would need to be in
# a group matching docker.sock's host GID to use it anyway, at which point the
# non-root separation buys little — docker-group access is root-equivalent on
# the host regardless. This is the same trade-off the update mechanism's
# original design made; see docs/README for the operator-facing writeup.
RUN apk add --no-cache git ca-certificates docker-cli docker-cli-compose
ARG TARGETARCH=amd64
COPY backend/bin/pubobs-linux-${TARGETARCH} /usr/local/bin/pubobs
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pubobs"]
