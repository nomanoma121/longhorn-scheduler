// longhorn-scheduler
// Copyright (c) 2026-present NAVER Corp.
// Apache-2.0

package pkg

import (
	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

var nodeReservationDuration = metrics.NewHistogram(&metrics.HistogramOpts{
	Namespace: "longhorn",
	Subsystem: "scheduler",
	Name:      "node_reservation_duration_seconds",
	Help:      "Duration a node was reserved while waiting for Longhorn replicas to be scheduled.",
	Buckets:   metrics.ExponentialBuckets(0.001, 2, 14),
})

func init() {
	legacyregistry.MustRegister(nodeReservationDuration)
}
