# syntax=docker/dockerfile:1
# Slidekick: one static Go binary with the web app embedded, on distroless.

FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
COPY schema/ /src/schema/
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ARG VERSION=0.1.0-dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/slidekick ./cmd/slidekick

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/slidekick /usr/local/bin/slidekick
ENV SLIDEKICK_ADDR=:7410 SLIDEKICK_DATA=/data
VOLUME /data
EXPOSE 7410
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD ["/usr/local/bin/slidekick", "health"]
ENTRYPOINT ["/usr/local/bin/slidekick"]
CMD ["serve"]
