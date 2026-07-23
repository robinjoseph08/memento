# syntax=docker/dockerfile:1.7

FROM node:24.13.0-alpine3.23 AS frontend
WORKDIR /src
RUN npm install --global pnpm@11.16.0
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY app ./app
COPY public ./public
COPY tsconfig.json tsconfig.app.json tsconfig.node.json vite.config.ts ./
RUN pnpm build

FROM golang:1.25.5-alpine3.23 AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memento ./cmd/api

FROM caddy:2.10.2-alpine
COPY --from=frontend /src/dist /srv/memento
COPY --from=backend /out/memento /usr/local/bin/memento
COPY Caddyfile /etc/caddy/Caddyfile
COPY deploy/entrypoint.sh /usr/local/bin/memento-entrypoint
RUN addgroup -S -g 10001 memento \
  && adduser -S -D -u 10001 -G memento -h /home/memento memento \
  && chown -R memento:memento /config /data /home/memento
USER memento
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/api/health/live || exit 1
ENTRYPOINT ["/usr/local/bin/memento-entrypoint"]
