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
RUN addgroup -S tproxy && adduser -S -G tproxy tproxy
WORKDIR /app
COPY --from=gateway /out/tproxy /usr/local/bin/tproxy
COPY config.example.yaml /app/config.yaml
RUN mkdir -p /data && chown -R tproxy:tproxy /app /data
USER tproxy
EXPOSE 28120
ENTRYPOINT ["tproxy"]
CMD ["--config", "/app/config.yaml"]
