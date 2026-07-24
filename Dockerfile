# syntax=docker/dockerfile:1.7

FROM golang:1.26.5-alpine3.23 AS go-base
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg

FROM go-base AS typegen
COPY tygo.yaml ./
RUN go tool tygo generate

FROM node:24.18.0-alpine3.23 AS frontend
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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/memento ./cmd/api

FROM caddy:2.10.2-alpine
COPY --from=frontend /src/dist /srv/memento
COPY --from=backend /out/memento /usr/local/bin/memento
COPY Caddyfile /etc/caddy/Caddyfile
COPY deploy/entrypoint.sh /usr/local/bin/memento-entrypoint
COPY deploy/healthcheck.sh /usr/local/bin/memento-healthcheck
RUN addgroup -S -g 10001 memento \
  && adduser -S -D -u 10001 -G memento -h /home/memento memento \
  && chown -R memento:memento /config /data /home/memento
USER memento
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["memento-healthcheck"]
ENTRYPOINT ["/usr/local/bin/memento-entrypoint"]
