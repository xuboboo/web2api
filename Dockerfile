# Build
# golang:1.26.11-bookworm (linux/amd64), digest pinned 2026-11-14.
FROM --platform=$BUILDPLATFORM golang:1.26.11-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /app/web2api .

# `chromedp/headless-shell:stable` also works here
FROM chromedp/headless-shell:stable@sha256:f7e7ac721b023cb8717f8108aef8b3e49995fb1e5a912f41e570c29e45d24961

WORKDIR /usr/src/app

# wget: used by HEALTHCHECK; ca-certificates validate outbound TLS requests.
# tini: reaps orphaned Chrome children, which would otherwise pile up as zombies when web2api is PID 1.
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates wget tini \
  && rm -rf /var/lib/apt/lists/* \
  && (getent passwd chrome >/dev/null 2>&1 || useradd --create-home --uid 1001 --shell /bin/bash chrome)
RUN mkdir -p /usr/src/app/data \
  && chown chrome:chrome /usr/src/app /usr/src/app/data

COPY --from=builder /app/web2api /usr/local/bin/web2api
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

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget --quiet --tries=1 --spider "http://127.0.0.1:${WEB2API_SERVER_PORT}/web-tool/health" || exit 1

# -s keeps reaping when another init is PID 1 (`docker run --init`, compose `init: true`).
ENTRYPOINT ["/usr/bin/tini", "-s", "--", "web2api"]
