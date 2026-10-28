# Home Assistant app image for ReoStream.
#
# The Go build stage is pinned to the machine doing the build and
# cross-compiles to the target architecture. ReoStream is pure Go with
# CGO disabled, so this avoids running the whole toolchain under QEMU.
ARG BUILD_FROM=ghcr.io/home-assistant/base:3.23
ARG BUILDPLATFORM=linux/amd64
FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS build

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev

WORKDIR /src

COPY go.mod go.sum ./
# go.mod replaces gortsplib with the patched copy in third_party, so it has
# to be present before go mod download runs.
COPY third_party ./third_party
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN set -eux; \
    target_os="${TARGETOS:-linux}"; \
    target_arch="${TARGETARCH:-amd64}"; \
    target_arm="${TARGETVARIANT#v}"; \
    CGO_ENABLED=0 GOOS="${target_os}" GOARCH="${target_arch}" GOARM="${target_arm}" \
      go build -trimpath \
      -ldflags="-s -w -X github.com/VoltTech21/reostream/internal/control.version=${VERSION}" \
      -o /out/reostream ./cmd/reostream

# Fix the service scripts' permissions while the build stage is still
# running natively. The arm64 and amd64 images then get identical files
# without a RUN instruction in the target architecture, which would need
# QEMU during a cross-build.
COPY rootfs /out/rootfs
RUN chmod a+x \
    /out/rootfs/etc/services.d/reostream/run \
    /out/rootfs/etc/services.d/reostream/finish

FROM ${BUILD_FROM}

COPY --from=build /out/reostream /usr/local/bin/reostream
COPY --from=build /out/rootfs /

# Give the daemon time to send its stream-stop messages to every camera.
# Killing it early leaves cameras refusing new sessions for several minutes.
ENV S6_SERVICES_GRACETIME=15000 \
    S6_KILL_GRACETIME=5000

LABEL \
    org.opencontainers.image.title="Home Assistant App: ReoStream" \
    org.opencontainers.image.description="Reolink camera proxy with clean RTSP and HTTP streams" \
    org.opencontainers.image.source="https://github.com/YamanKoudmani/reostream-ha" \
    org.opencontainers.image.licenses="AGPL-3.0-only"

# 8560 HTTP streams and status, 8561 RTSP, 8562 control panel,
# 2121-2131 FTP clip receiver.
EXPOSE 8560 8561 8562 2121-2131

STOPSIGNAL SIGTERM
