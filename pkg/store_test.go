// longhorn-scheduler
// Copyright (c) 2026-present NAVER Corp.
// Apache-2.0

package pkg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestParseNodeSpec(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		expected nodeSpec
	}{
		{
			name: "full spec",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"spec": map[string]interface{}{
					"allowScheduling":   true,
					"evictionRequested": false,
					"tags":              []interface{}{"storage", "fast"},
					"disks": map[string]interface{}{
						"disk-1": map[string]interface{}{
							"allowScheduling": true,
							"storageReserved": int64(1000),
							"tags":            []interface{}{"ssd"},
						},
						"disk-2": map[string]interface{}{
							"allowScheduling": false,
							"storageReserved": int64(2000),
						},
					},
				},
			}},
			expected: nodeSpec{
				AllowScheduling:   true,
				EvictionRequested: false,
				Tags:              []string{"storage", "fast"},
				Disks: map[string]diskSpec{
					"disk-1": {AllowScheduling: true, StorageReserved: 1000, Tags: []string{"ssd"}},
					"disk-2": {AllowScheduling: false, StorageReserved: 2000},
				},
			},
		},
		{
			name:     "nil object",
			obj:      &unstructured.Unstructured{Object: nil},
			expected: nodeSpec{Disks: map[string]diskSpec{}},
		},
		{
			name:     "empty object",
			obj:      &unstructured.Unstructured{Object: map[string]interface{}{}},
			expected: nodeSpec{Disks: map[string]diskSpec{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseNodeSpec(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseNodeStatus(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		expected nodeStatus
	}{
		{
			name: "full status",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{"type": "Ready", "status": "True"},
						map[string]interface{}{"type": "Schedulable", "status": "False"},
					},
					"diskStatus": map[string]interface{}{
						"disk-1": map[string]interface{}{
							"storageMaximum":   int64(100000),
							"storageScheduled": int64(30000),
							"scheduledReplica": map[string]interface{}{
								"replica-1": int64(5368709120),
								"replica-2": int64(5368709120),
							},
						},
						"disk-2": map[string]interface{}{
							"storageMaximum":   int64(200000),
							"storageScheduled": int64(0),
						},
					},
				},
			}},
			expected: nodeStatus{
				Conditions: []condition{
					{Type: "Ready", Status: "True"},
					{Type: "Schedulable", Status: "False"},
				},
				DiskStatus: map[string]diskStatus{
					"disk-1": {
						StorageMaximum:   100000,
						StorageScheduled: 30000,
						ScheduledReplica: map[string]bool{"replica-1": true, "replica-2": true},
					},
					"disk-2": {
						StorageMaximum:   200000,
						StorageScheduled: 0,
					},
				},
			},
		},
		{
			name:     "nil object",
			obj:      &unstructured.Unstructured{Object: nil},
			expected: nodeStatus{DiskStatus: map[string]diskStatus{}},
		},
		{
			name:     "empty object",
			obj:      &unstructured.Unstructured{Object: map[string]interface{}{}},
			expected: nodeStatus{DiskStatus: map[string]diskStatus{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseNodeStatus(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseVolumeInfo(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		expected volumeInfo
	}{
		{
			name: "full volumeInfo",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"metadata": map[string]interface{}{"name": "vol-1"},
				"spec": map[string]interface{}{
					"size":         "10737418240",
					"nodeSelector": []interface{}{"node-a"},
					"diskSelector": []interface{}{"ssd", "nvme"},
				},
			}},
			expected: volumeInfo{
				Name:         "vol-1",
				Size:         10737418240,
				NodeSelector: []string{"node-a"},
				DiskSelector: []string{"ssd", "nvme"},
			},
		},
		{
			name: "empty spec",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"metadata": map[string]interface{}{"name": "vol-2"},
			}},
			expected: volumeInfo{
				Name: "vol-2",
			},
		},
		{
			name:     "nil object",
			obj:      &unstructured.Unstructured{Object: nil},
			expected: volumeInfo{},
		},
		{
			name:     "empty object",
			obj:      &unstructured.Unstructured{Object: map[string]interface{}{}},
			expected: volumeInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseVolumeInfo(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetReplicaNodeID(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		expected string
	}{
		{
			name: "has nodeID",
			obj: &unstructured.Unstructured{Object: map[string]interface{}{
				"spec": map[string]interface{}{"nodeID": "node-1"},
			}},
			expected: "node-1",
		},
		{
			name:     "empty object",
			obj:      &unstructured.Unstructured{Object: map[string]interface{}{}},
			expected: "",
		},
		{
			name:     "nil object",
			obj:      &unstructured.Unstructured{Object: nil},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getReplicaNodeID(tt.obj)
			assert.Equal(t, tt.expected, result)
		})
	}
}
