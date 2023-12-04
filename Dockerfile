# syntax=docker/dockerfile:1

# source: https://github.com/vkd/Makefile

ARG _GOVERSION

FROM --platform=$BUILDPLATFORM golang:${_GOVERSION:+${_GOVERSION}-}alpine AS builder
RUN apk update && apk add --no-cache git ca-certificates tzdata make && update-ca-certificates

# Create appuser
ENV USER=appuser
ENV UID=10001

# See https://stackoverflow.com/a/55757473/12429735
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "${UID}" \
    "${USER}"

WORKDIR /workspace
COPY go.mod /workspace/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    go mod download

COPY . /workspace

ARG _VERSION
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    BUILD_OUTPUT=/workspace/out VERSION=${_VERSION} make build


FROM alpine
# Import from builder.
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

WORKDIR /app
COPY --from=builder /workspace/out /app/out

# Use an unprivileged user.
USER appuser:appuser

ENTRYPOINT ["/app/out"]
