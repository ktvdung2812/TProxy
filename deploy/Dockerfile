FROM alpine:3.22

ARG TARGETARCH

RUN addgroup -S -g 10001 tproxy && adduser -S -D -H -u 10001 -G tproxy tproxy
WORKDIR /app

COPY bin/tproxy-linux-${TARGETARCH} /usr/local/bin/tproxy
COPY config.yaml /app/config.yaml

RUN chmod +x /usr/local/bin/tproxy \
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
