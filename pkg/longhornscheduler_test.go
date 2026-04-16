// longhorn-scheduler
// Copyright (c) 2026-present NAVER Corp.
// Apache-2.0

package pkg

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestFindOriginalNode(t *testing.T) {
	tests := []struct {
		name         string
		volumes      []volumeInfo
		replicaNodes map[string]map[string]bool
		expected     string
	}{
		{
			name:         "no volumes",
			volumes:      nil,
			replicaNodes: map[string]map[string]bool{},
			expected:     "",
		},
		{
			name:    "no replicas",
			volumes: []volumeInfo{{Name: "vol-1"}},
			replicaNodes: map[string]map[string]bool{
				"vol-1": {},
			},
			expected: "",
		},
		{
			name:    "single volume on one node",
			volumes: []volumeInfo{{Name: "vol-1"}},
			replicaNodes: map[string]map[string]bool{
				"vol-1": {"node-a": true},
			},
			expected: "node-a",
		},
		{
			name: "all volumes on same node",
			volumes: []volumeInfo{
				{Name: "vol-1"},
				{Name: "vol-2"},
			},
			replicaNodes: map[string]map[string]bool{
				"vol-1": {"node-a": true, "node-b": true},
				"vol-2": {"node-a": true},
			},
			expected: "node-a",
		},
		{
			name: "volumes spread across nodes",
			volumes: []volumeInfo{
				{Name: "vol-1"},
				{Name: "vol-2"},
			},
			replicaNodes: map[string]map[string]bool{
				"vol-1": {"node-a": true},
				"vol-2": {"node-b": true},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findOriginalNode(tt.volumes, tt.replicaNodes)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsSelectorsInTags

func TestIsSelectorsInTags(t *testing.T) {
	tests := []struct {
		name       string
		tags       []string
		selectors  []string
		allowEmpty bool
		expected   bool
	}{
		{
			name:       "empty selectors, allow empty",
			tags:       []string{"ssd"},
			selectors:  nil,
			allowEmpty: true,
			expected:   true,
		},
		{
			name:       "empty selectors, disallow empty",
			tags:       []string{"ssd"},
			selectors:  nil,
			allowEmpty: false,
			expected:   false,
		},
		{
			name:       "all selectors matched",
			tags:       []string{"ssd", "fast", "nvme"},
			selectors:  []string{"ssd", "fast"},
			allowEmpty: false,
			expected:   true,
		},
		{
			name:       "one selector missing",
			tags:       []string{"ssd"},
			selectors:  []string{"ssd", "nvme"},
			allowEmpty: false,
			expected:   false,
		},
		{
			name:       "no tags, non-empty selectors",
			tags:       nil,
			selectors:  []string{"ssd"},
			allowEmpty: false,
			expected:   false,
		},
		{
			name:       "exact match",
			tags:       []string{"storage"},
			selectors:  []string{"storage"},
			allowEmpty: false,
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSelectorsInTags(tt.tags, tt.selectors, tt.allowEmpty)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetConditionStatus(t *testing.T) {
	conditions := []condition{
		{Type: "Ready", Status: "True"},
		{Type: "Schedulable", Status: "False"},
	}

	assert.Equal(t, "True", getConditionStatus(conditions, "Ready"))
	assert.Equal(t, "False", getConditionStatus(conditions, "Schedulable"))
	assert.Equal(t, "", getConditionStatus(conditions, "Unknown"))
	assert.Equal(t, "", getConditionStatus(nil, "Ready"))
}

func TestHasPodSchedulingFailure(t *testing.T) {
	tests := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{
			name:     "no conditions",
			pod:      &corev1.Pod{},
			expected: false,
		},
		{
			name: "PodScheduled=True",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
					},
				},
			},
			expected: false,
		},
		{
			name: "PodScheduled=False",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodScheduled, Status: corev1.ConditionFalse},
					},
				},
			},
			expected: true,
		},
		{
			name: "unrelated condition only",
			pod: &corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{Type: corev1.PodReady, Status: corev1.ConditionFalse},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, hasPodSchedulingFailure(tt.pod))
		})
	}
}

func TestBinPacking(t *testing.T) {
	const gb = int64(1 << 30)

	makeDisk := func(allowScheduling bool, max, reserved, scheduled int64, tags ...string) (diskSpec, diskStatus) {
		return diskSpec{
				AllowScheduling: allowScheduling,
				StorageReserved: reserved,
				Tags:            tags,
			}, diskStatus{
				StorageMaximum:   max,
				StorageScheduled: scheduled,
			}
	}

	tests := []struct {
		name                   string
		spec                   nodeSpec
		status                 nodeStatus
		volumes                []volumeInfo
		allowEmptyDiskSelector bool
		overProvisioningPct    int64
		expected               bool
	}{
		{
			name: "single volume fits",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 100*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 100*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 10 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               true,
		},
		{
			name: "volume exceeds capacity",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 10*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 10*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 20 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               false,
		},
		{
			name: "two volumes fit across two disks",
			spec: func() nodeSpec {
				sp1, _ := makeDisk(true, 20*gb, 0, 0)
				sp2, _ := makeDisk(true, 20*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp1, "disk-1": sp2}}
			}(),
			status: func() nodeStatus {
				_, st1 := makeDisk(true, 20*gb, 0, 0)
				_, st2 := makeDisk(true, 20*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st1, "disk-1": st2}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 15 * gb}, {Name: "vol-2", Size: 15 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               true,
		},
		{
			name: "already scheduled reduces available space",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 20*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 20*gb, 0, 15*gb)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 10 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               false,
		},
		{
			name: "over-provisioning at 200% doubles capacity",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 10*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 10*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 15 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    200,
			expected:               true,
		},
		{
			name: "disk selector matched",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 100*gb, 0, 0, "ssd")
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 100*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 10 * gb, DiskSelector: []string{"ssd"}}},
			allowEmptyDiskSelector: false,
			overProvisioningPct:    100,
			expected:               true,
		},
		{
			name: "disk selector not matched",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 100*gb, 0, 0, "hdd")
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 100*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 10 * gb, DiskSelector: []string{"ssd"}}},
			allowEmptyDiskSelector: false,
			overProvisioningPct:    100,
			expected:               false,
		},
		{
			name: "disk scheduling disabled",
			spec: func() nodeSpec {
				sp, _ := makeDisk(false, 100*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(false, 100*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 10 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               false,
		},
		{
			name: "reserved storage reduces schedulable space",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 20*gb, 15*gb, 0)
				return nodeSpec{Disks: map[string]diskSpec{"disk-0": sp}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 20*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 10 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               false,
		},
		{
			name: "disk in status but not in spec is skipped",
			spec: nodeSpec{
				Disks: map[string]diskSpec{},
			},
			status: func() nodeStatus {
				_, st := makeDisk(true, 100*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": st}}
			}(),
			volumes:                []volumeInfo{{Name: "vol-1", Size: 10 * gb}},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               false,
		},
		{
			// 4 vols × 4 disks: ssd/hdd tags force specific assignments;
			// each disk holds exactly one 25 GB volume (30 GB capacity).
			name: "4 volumes, 4 disks — tag-constrained assignment fits",
			spec: func() nodeSpec {
				sp0, _ := makeDisk(true, 30*gb, 0, 0, "ssd", "fast")
				sp1, _ := makeDisk(true, 30*gb, 0, 0, "ssd")
				sp2, _ := makeDisk(true, 30*gb, 0, 0, "hdd", "fast")
				sp3, _ := makeDisk(true, 30*gb, 0, 0, "hdd")
				return nodeSpec{Disks: map[string]diskSpec{
					"disk-0": sp0, "disk-1": sp1, "disk-2": sp2, "disk-3": sp3,
				}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 30*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{
					"disk-0": st, "disk-1": st, "disk-2": st, "disk-3": st,
				}}
			}(),
			volumes: []volumeInfo{
				{Name: "vol-1", Size: 25 * gb, DiskSelector: []string{"ssd"}},
				{Name: "vol-2", Size: 25 * gb, DiskSelector: []string{"ssd"}},
				{Name: "vol-3", Size: 25 * gb, DiskSelector: []string{"hdd"}},
				{Name: "vol-4", Size: 25 * gb, DiskSelector: []string{"hdd"}},
			},
			allowEmptyDiskSelector: false,
			overProvisioningPct:    100,
			expected:               true,
		},
		{
			// 4 vols × 3 disks: two 25 GB vols each claim a disk,
			// leaving one 30 GB disk to hold both 12 GB vols (24 GB total).
			// Backtracking is required to discover this valid packing.
			name: "4 volumes, 3 disks — two small volumes share one disk",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 30*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{
					"disk-0": sp, "disk-1": sp, "disk-2": sp,
				}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 30*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{
					"disk-0": st, "disk-1": st, "disk-2": st,
				}}
			}(),
			volumes: []volumeInfo{
				{Name: "vol-1", Size: 25 * gb},
				{Name: "vol-2", Size: 25 * gb},
				{Name: "vol-3", Size: 12 * gb},
				{Name: "vol-4", Size: 12 * gb},
			},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               true,
		},
		{
			// 4 vols × 3 disks: total needed (81 GB) < total available (90 GB),
			// but the two 25 GB vols each need their own disk, leaving only 5 GB
			// scraps — not enough to fit the 19 GB and 12 GB vols separately.
			name: "4 volumes, 3 disks — total capacity OK but no valid bin-packing",
			spec: func() nodeSpec {
				sp, _ := makeDisk(true, 30*gb, 0, 0)
				return nodeSpec{Disks: map[string]diskSpec{
					"disk-0": sp, "disk-1": sp, "disk-2": sp,
				}}
			}(),
			status: func() nodeStatus {
				_, st := makeDisk(true, 30*gb, 0, 0)
				return nodeStatus{DiskStatus: map[string]diskStatus{
					"disk-0": st, "disk-1": st, "disk-2": st,
				}}
			}(),
			volumes: []volumeInfo{
				{Name: "vol-1", Size: 25 * gb},
				{Name: "vol-2", Size: 25 * gb},
				{Name: "vol-3", Size: 19 * gb},
				{Name: "vol-4", Size: 12 * gb},
			},
			allowEmptyDiskSelector: true,
			overProvisioningPct:    100,
			expected:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := binPacking(tt.spec, tt.status, tt.volumes, tt.allowEmptyDiskSelector, tt.overProvisioningPct)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestAllReplicasScheduled(t *testing.T) {
	tests := []struct {
		name     string
		status   nodeStatus
		expected map[string]bool
		want     bool
	}{
		{
			name: "all replicas scheduled",
			status: nodeStatus{
				DiskStatus: map[string]diskStatus{
					"disk-0": {ScheduledReplica: map[string]bool{"replica-1": true, "replica-2": true}},
				},
			},
			expected: map[string]bool{"replica-1": true, "replica-2": true},
			want:     true,
		},
		{
			name: "replica missing",
			status: nodeStatus{
				DiskStatus: map[string]diskStatus{
					"disk-0": {ScheduledReplica: map[string]bool{"replica-1": true}},
				},
			},
			expected: map[string]bool{"replica-1": true, "replica-2": true},
			want:     false,
		},
		{
			name: "replicas across multiple disks",
			status: nodeStatus{
				DiskStatus: map[string]diskStatus{
					"disk-0": {ScheduledReplica: map[string]bool{"replica-1": true}},
					"disk-1": {ScheduledReplica: map[string]bool{"replica-2": true}},
				},
			},
			expected: map[string]bool{"replica-1": true, "replica-2": true},
			want:     true,
		},
		{
			name:     "empty expected always true",
			status:   nodeStatus{DiskStatus: map[string]diskStatus{}},
			expected: map[string]bool{},
			want:     true,
		},
		{
			name:     "no scheduled replicas",
			status:   nodeStatus{DiskStatus: map[string]diskStatus{"disk-0": {}}},
			expected: map[string]bool{"replica-1": true},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, allReplicasScheduled(tt.status, tt.expected))
		})
	}
}
