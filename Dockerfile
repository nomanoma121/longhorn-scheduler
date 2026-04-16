# longhorn-scheduler
# Copyright (c) 2026-present NAVER Corp.
# Apache-2.0

FROM golang:1.25 AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o longhorn-scheduler ./cmd/scheduler

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /build/longhorn-scheduler /longhorn-scheduler

ENTRYPOINT ["/longhorn-scheduler"]
