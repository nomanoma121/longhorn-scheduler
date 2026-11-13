# longhorn-scheduler
# Copyright (c) 2026-present NAVER Corp.
# Apache-2.0

# Build on the native runner platform and cross-compile for the target,
# so multi-arch builds don't need QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.25 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o longhorn-scheduler ./cmd/scheduler

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /build/longhorn-scheduler /longhorn-scheduler

ENTRYPOINT ["/longhorn-scheduler"]
