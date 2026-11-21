# syntax=docker/dockerfile:1
# Slidekick: one static Go binary with the web app embedded, on distroless.

# Base images are pinned to their multi-arch index digest (tag kept for
# readability). Dependabot (.github/dependabot.yml) proposes updates weekly; to
# refresh by hand, run `docker buildx imagetools inspect <image:tag>` and copy
# the top-level Digest (the index, not a per-platform manifest).

# Web and Go stages run on the build machine's platform and cross-compile, so
# multi-arch builds (buildx --platform linux/amd64,linux/arm64) stay fast.
FROM --platform=$BUILDPLATFORM node:26-alpine@sha256:6bd9ab43c0ec0190ca7b2d193906cb8b7edf6e7f4afa43ddeccd6452710bffb5 AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
COPY schema/ /src/schema/
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=0.1.0-dev
# COMMIT is optional (the build context has no .git); the build date is stamped here.
ARG COMMIT=
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.built=$(date -u +%Y-%m-%d)" -o /out/slidekick ./cmd/slidekick

# ---- render: adds headless Chromium for render_slide (tag: slidekick:<ver>-render)
FROM debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS render
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium fonts-noto-core fonts-noto-color-emoji fonts-liberation ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/slidekick /usr/local/bin/slidekick
ENV SLIDEKICK_ADDR=:7410 SLIDEKICK_DATA=/data SLIDEKICK_CHROME=/usr/bin/chromium HOME=/tmp
USER 65532:65532
VOLUME /data
EXPOSE 7410
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/usr/local/bin/slidekick", "health"]
ENTRYPOINT ["/usr/local/bin/slidekick"]
CMD ["serve"]

LABEL org.opencontainers.image.source="https://github.com/SecOps-7/slidekick" org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.description="Slidekick with headless Chromium for render_slide and PDF/PNG export"

# ---- default: minimal distroless image (render_slide reports Chromium is missing)
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/slidekick /usr/local/bin/slidekick
ENV SLIDEKICK_ADDR=:7410 SLIDEKICK_DATA=/data
VOLUME /data
EXPOSE 7410
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/usr/local/bin/slidekick", "health"]
ENTRYPOINT ["/usr/local/bin/slidekick"]
CMD ["serve"]
LABEL org.opencontainers.image.source="https://github.com/SecOps-7/slidekick" org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.description="Slidekick: agents author animated slide decks over MCP; humans watch, edit and present live"
