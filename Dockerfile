# syntax=docker/dockerfile:1.7

FROM golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS typegen
WORKDIR /src
RUN go install github.com/gzuidhof/tygo@v0.2.21
COPY go.mod go.sum tygo.yaml ./
COPY pkg ./pkg
RUN tygo generate

FROM node:26.8.1-alpine@sha256:2d984a15c9b54fd0aeb608b8e0d0d83529eb34d2966db27a1fb4f1edc3d298a3 AS frontend
WORKDIR /src
RUN npm install --global pnpm@11.25.0
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY app ./app
COPY tsconfig*.json vite.config.ts ./
COPY --from=typegen /src/app/types/generated ./app/types/generated
RUN pnpm build

FROM golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS backend
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY --from=frontend /src/internal/webapp/dist ./internal/webapp/dist
RUN MODULE=$(go list -m) && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -installsuffix cgo \
      -ldflags "-w -s -X $MODULE/pkg/version.Version=$VERSION" \
      -o /out/app ./cmd/api

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S -G app app && \
    mkdir -p /config /data/files && chown -R app:app /config /data
COPY --from=backend --chown=app:app /out/app /usr/local/bin/app
USER app
ENV FILES_PATH=/data/files \
    SERVER_HOST=0.0.0.0 \
    SERVER_PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD wget --quiet --output-document=/dev/null http://127.0.0.1:8080/health || exit 1
ENTRYPOINT ["/usr/local/bin/app"]
