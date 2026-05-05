FROM alpine:3.21
RUN apk add --no-cache git ca-certificates
ARG TARGETARCH=amd64
COPY backend/bin/pubobs-linux-${TARGETARCH} /usr/local/bin/pubobs
RUN adduser -D -u 1000 pubobs
USER pubobs
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pubobs"]
