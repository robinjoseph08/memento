# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM golang:1.26.5-alpine3.23@sha256:622e56dbc11a8cfe87cafa2331e9a201877271cbff918af53d3be315f3da88cc AS go-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal ./internal
COPY pkg ./pkg

FROM go-base AS typegen
COPY tygo.yaml ./
RUN go tool tygo generate

FROM node:24.18.0-alpine3.23@sha256:595398b0081eacda8e1c4c5b97b76cd1020e4d58a8ebcb4843b9bca1e79e7436 AS frontend
WORKDIR /src
RUN npm install --global pnpm@11.16.0
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY public ./public
COPY tsconfig.json tsconfig.app.json tsconfig.node.json vite.config.ts ./
COPY app ./app
COPY --from=typegen /src/app/types/generated ./app/types/generated
RUN pnpm build

FROM go-base AS backend
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memento ./cmd/api \
  && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memento-migrations ./cmd/migrations

FROM caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
ARG MEMENTO_VERSION=dev
ARG MEMENTO_REVISION=unknown
ARG MEMENTO_SOURCE=https://github.com/robinjoseph08/memento
ARG MEMENTO_CREATED=1970-01-01T00:00:00Z
LABEL org.opencontainers.image.title="Memento" \
  org.opencontainers.image.description="Private family media publishing from Immich" \
  org.opencontainers.image.source="$MEMENTO_SOURCE" \
  org.opencontainers.image.revision="$MEMENTO_REVISION" \
  org.opencontainers.image.version="$MEMENTO_VERSION" \
  org.opencontainers.image.created="$MEMENTO_CREATED" \
  org.opencontainers.image.licenses="MIT"
COPY --from=frontend /src/dist /srv/memento
COPY --from=backend /out/memento /usr/local/bin/memento
COPY --from=backend /out/memento-migrations /usr/local/bin/memento-migrations
COPY Caddyfile /etc/caddy/Caddyfile
COPY deploy/entrypoint.sh /usr/local/bin/memento-entrypoint
COPY deploy/healthcheck.sh /usr/local/bin/memento-healthcheck
COPY LICENSE /usr/share/licenses/memento/LICENSE
RUN addgroup -S -g 10001 memento \
  && adduser -S -D -u 10001 -G memento -h /home/memento memento \
  && chown -R memento:memento /config /data /home/memento
USER memento
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["memento-healthcheck"]
ENTRYPOINT ["/usr/local/bin/memento-entrypoint"]
