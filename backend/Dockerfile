# Stage 1: Build frontend TypeScript
FROM node:22-alpine AS frontend
WORKDIR /app
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ .
RUN mkdir -p /backend/frontend/static
RUN node esbuild.config.mjs production

# Stage 2: Build Go backend (embeds frontend output)
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
COPY --from=frontend /backend/frontend/static/ ./frontend/static/
RUN CGO_ENABLED=0 go build -o /pubobs ./cmd/server

# Stage 3: Runtime
FROM alpine:3.21
RUN apk add --no-cache git ca-certificates
COPY --from=builder /pubobs /usr/local/bin/pubobs
RUN adduser -D -u 1000 pubobs
USER pubobs
VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/pubobs"]
