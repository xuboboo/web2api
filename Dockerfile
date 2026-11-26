# Build
# golang:1.26.11-bookworm (linux/amd64), digest pinned 2026-11-14.
FROM --platform=$BUILDPLATFORM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS builder

WORKDIR /build

# 部署环境无法直连 proxy.golang.org（中国大陆），改用 goproxy.cn 镜像。
# 其余构建步骤与原 Dockerfile 完全一致。
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /app/web2api .

# Multi-architecture stable index verified 2026-11-26 (amd64 and arm64).
FROM chromedp/headless-shell:stable@sha256:381353d3530ab9d579d2fe8b9c0f4519c995617f5a71daf6bb649e2d12005bd9

WORKDIR /usr/src/app

# 部署主机连 deb.debian.org 的 apt 极不稳定（实测挂起 >5 分钟），改为从 builder 阶段
# 直接拷贝 CA 根证书（等价 ca-certificates），省掉整段 apt-get。
# tini 由 compose 的 init: true 提供（/usr/bin/docker-init）；healthcheck 用 bash 的
# /dev/tcp 探测，不再依赖 wget。
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN mkdir -p /usr/src/app/data \
  && (getent passwd chrome >/dev/null 2>&1 || useradd --create-home --uid 1001 --shell /bin/bash chrome) \
  && chown chrome:chrome /usr/src/app /usr/src/app/data

COPY --from=builder /app/web2api /usr/local/bin/web2api
COPY LICENSE /usr/share/doc/web2api/LICENSE
COPY --chown=chrome:chrome config.yaml ./config.yaml
# Static panels served by the serve command: the marketing site at /web-tool/
# (BasePath root) and the admin console at /web-tool/console/ (fixed sub-paths,
# see core.BasePath). The platform package (bbolt keys/usage store, JWT admin
# auth) is compiled into the binary above — no runtime files needed.
COPY --chown=chrome:chrome web/site ./web/site
COPY --chown=chrome:chrome web/admin ./web/admin

# Rod's launcher.LookPath does not know about /headless-shell/headless-shell.
# Viper auto-binds WEB2API_APP_BROWSER_PATH to app.browser_path, so this pins the binary and avoids Rod's runtime chromium auto-download (which would fail in this non-root, network-restricted image).
ENV WEB2API_APP_BROWSER_PATH=/headless-shell/headless-shell \
  WEB2API_SERVER_HOST=0.0.0.0 \
  WEB2API_SERVER_PORT=7000

USER chrome

# 运行层没有 wget（见上），用 bash /dev/tcp 探测 HTTP 200。
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD bash -c "exec 3<>/dev/tcp/127.0.0.1/${WEB2API_SERVER_PORT} && printf 'GET /web-tool/health HTTP/1.0\r\n\r\n' >&3 && head -n 1 <&3 | grep -q ' 200 '"

# -s keeps reaping when another init is PID 1 (`docker run --init`, compose `init: true`).
# 僵尸回收交给 compose 的 init: true（/usr/bin/docker-init），镜像内不再装 tini。
ENTRYPOINT ["web2api"]
