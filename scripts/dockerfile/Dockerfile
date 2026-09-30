# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26.4
ARG NODE_VERSION=24
ARG ALPINE_VERSION=3.23

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS frontend
WORKDIR /src/web

RUN corepack enable && corepack prepare pnpm@11.10.0 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile

COPY web/ ./
ARG APP_VERSION=dev
ARG GITHUB_REPO=https://github.com/xiaocqaq/octopus
ENV VITE_APP_VERSION=$APP_VERSION \
    VITE_GITHUB_REPO=$GITHUB_REPO
RUN pnpm run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS backend
WORKDIR /src

ARG TARGETPLATFORM
ARG APP_VERSION=dev
ARG GITHUB_REPO=https://github.com/xiaocqaq/octopus
ARG BUILD_TIME=1970-01-01T00:00:00Z
ARG LDFLAGS="-s -w -X github.com/bestruirui/octopus/internal/conf.Version=${APP_VERSION} -X github.com/bestruirui/octopus/internal/conf.BuildTime=${BUILD_TIME} -X github.com/bestruirui/octopus/internal/conf.Repo=${GITHUB_REPO}"

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=bind,from=frontend,source=/src/static/out,target=/src/static/out,readonly \
    GOOS="$(echo "${TARGETPLATFORM}" | cut -d/ -f1)" \
    GOARCH="$(echo "${TARGETPLATFORM}" | cut -d/ -f2)" \
    GOARM="$(case "${TARGETPLATFORM}" in linux/arm/*) echo "${TARGETPLATFORM}" | cut -d/ -f3;; esac)" \
    CGO_ENABLED=0 go build -trimpath -tags=jsoniter -o /out/octopus \
    -ldflags="${LDFLAGS}" .

FROM alpine:${ALPINE_VERSION}

ENV APP_NAME=octopus \
    TZ=UTC

RUN apk add --no-cache ca-certificates su-exec tzdata && \
    mkdir -p /app/data && chown -R 0:0 /app

COPY --from=backend /out/octopus /app/octopus
COPY scripts/dockerfile/entrypoint.sh /entrypoint.sh
COPY LICENSE /app/LICENSE

RUN chmod +x /app/octopus /entrypoint.sh

EXPOSE 8080
VOLUME ["/app/data"]
ENTRYPOINT ["/entrypoint.sh"]
