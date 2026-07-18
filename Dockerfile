FROM node:22-alpine AS dashboard
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS gateway
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=dashboard /src/internal/api/dashboard ./internal/api/dashboard
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tproxy ./cmd/tproxy

FROM alpine:3.22
RUN addgroup -S -g 10001 tproxy && adduser -S -D -H -u 10001 -G tproxy tproxy
WORKDIR /app
COPY --from=gateway /out/tproxy /usr/local/bin/tproxy
COPY config.example.yaml /app/config.yaml
# The checked-in example stays loopback/relative-path safe for local use. The
# image rewrites only its private runtime copy so a container is reachable from
# the published port and its SQLite/WAL files live on the persistent volume.
RUN sed -i \
      -e 's/^  host: 127\.0\.0\.1$/  host: 0.0.0.0/' \
    -e 's#^  dsn: tproxy\.db$#  dsn: /data/tproxy.db#' \
      /app/config.yaml \
    && grep -q '^  host: 0.0.0.0$' /app/config.yaml \
    && grep -q '^  dsn: /data/tproxy.db$' /app/config.yaml \
    && mkdir -p /data \
    && chown -R tproxy:tproxy /app /data
USER tproxy
EXPOSE 28120
VOLUME ["/data"]
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:28120/healthz || exit 1
ENTRYPOINT ["tproxy"]
CMD ["--config", "/app/config.yaml"]
