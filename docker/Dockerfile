FROM golang:1.24-bookworm AS build

RUN apt-get update \
    && apt-get install -y --no-install-recommends build-essential pkg-config libfuse3-dev ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY . .

# Use Docker's automatic TARGETARCH variable for multi-arch support
ARG TARGETARCH
# CGO_ENABLED=1 is required for FUSE integration. 
# -pgo=auto ensures we use profile-guided optimization if default.pgo is present.
RUN CGO_ENABLED=1 GOOS=linux GOARCH=$TARGETARCH go build -pgo=auto -o /out/gostream .

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
      ca-certificates \
      fuse3 \
      iptables \
      python3 \
      python3-venv \
      tini \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt /tmp/requirements.txt
RUN python3 -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/venv/bin/pip install --no-cache-dir -r /tmp/requirements.txt \
    && rm -f /tmp/requirements.txt

COPY --from=build /out/gostream /usr/local/bin/gostream
COPY docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY scripts /app/scripts

RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV PATH=/opt/venv/bin:$PATH
ENV MKV_PROXY_CONFIG_PATH=/config.json
ENV GOSTREAM_ROOT_PATH=/usr/local
ENV GOSTREAM_SOURCE_PATH=/mnt/gostream-mkv-real
ENV GOSTREAM_MOUNT_PATH=/mnt/gostream-mkv-virtual
ENV GOSTREAM_STATE_DIR=/usr/local/STATE
ENV GOSTREAM_LOG_DIR=/usr/local/logs
ENV HEALTH_MONITOR_PORT=8095
ENV GOSTREAM_HEALTH_MONITOR_ENABLED=1

EXPOSE 8090 8095 8096

# tini is used as PID 1 to forward signals (e.g. SIGTERM on docker stop) to
# gostream and reap zombie processes. Without it, the shell entrypoint script
# would be PID 1 and would not forward signals, causing an unclean shutdown.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/docker-entrypoint.sh"]
