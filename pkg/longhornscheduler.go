// longhorn-scheduler
// Copyright (c) 2026-present NAVER Corp.
// Apache-2.0

package pkg

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	corelisters "k8s.io/client-go/listers/core/v1"
	storagelisters "k8s.io/client-go/listers/storage/v1"
	"k8s.io/klog/v2"
	"k8s.io/kube-scheduler/framework"
	frameworkruntime "k8s.io/kubernetes/pkg/scheduler/framework/runtime"
)

const (
	Name     = "LonghornScheduler"
	stateKey = Name + "State"

	longhornNamespace  = "longhorn-system"
	longhornDriverName = "driver.longhorn.io"

	settingAllowEmptyNodeSelector     = "allow-empty-node-selector-volume"
	settingAllowEmptyDiskSelector     = "allow-empty-disk-selector-volume"
	settingStorageOverProvisioningPct = "storage-over-provisioning-percentage"
)

// LonghornArgs holds the plugin configuration from the scheduler's pluginConfig.
type LonghornArgs struct {
	NodeReservationTimeoutSeconds int64 `json:"nodeReservationTimeoutSeconds"`
}

type LonghornScheduler struct {
	store              *longhornStore
	pvLister           corelisters.PersistentVolumeLister
	pvcLister          corelisters.PersistentVolumeClaimLister
	scLister           storagelisters.StorageClassLister
	reservationTimeout time.Duration
	reservedMu         sync.RWMutex
	reservedNodes      map[string]time.Time
}

var _ framework.PreFilterPlugin = &LonghornScheduler{}
var _ framework.FilterPlugin = &LonghornScheduler{}
var _ framework.ReservePlugin = &LonghornScheduler{}
var _ framework.PostBindPlugin = &LonghornScheduler{}

func (l *LonghornScheduler) Name() string { return Name }

func New(_ context.Context, obj runtime.Object, h framework.Handle) (framework.Plugin, error) {
	var args LonghornArgs
	if err := frameworkruntime.DecodeInto(obj, &args); err != nil {
		return nil, fmt.Errorf("failed to decode plugin args: %w", err)
	}

	store, err := newLonghornStore(h.KubeConfig(), longhornNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to init longhorn store: %w", err)
	}

	return &LonghornScheduler{
		store:              store,
		pvLister:           h.SharedInformerFactory().Core().V1().PersistentVolumes().Lister(),
		pvcLister:          h.SharedInformerFactory().Core().V1().PersistentVolumeClaims().Lister(),
		scLister:           h.SharedInformerFactory().Storage().V1().StorageClasses().Lister(),
		reservationTimeout: max(0, time.Duration(args.NodeReservationTimeoutSeconds)*time.Second),
		reservedNodes:      make(map[string]time.Time),
	}, nil
}

// longhornState holds per-scheduling-cycle data computed in PreFilter and
// consumed by Filter. It is stored in the framework's CycleState.
type longhornState struct {
	volumes      []volumeInfo
	replicaNodes map[string]map[string]bool // volumeName -> set of nodeIDs
	isPodRestart bool
	originalNode string

	allowEmptyNodeSelector bool
	allowEmptyDiskSelector bool
	overProvisioningPct    int64
}

func (s *longhornState) Clone() framework.StateData { return s }

// PreFilter resolves all Longhorn volumes attached to the pod, builds a map of
// which nodes already have replicas, and reads Longhorn settings. If all volumes
// already have replicas on a single node (pod restart case), it restricts
// scheduling to that node via PreFilterResult.
func (l *LonghornScheduler) PreFilter(ctx context.Context, state framework.CycleState, pod *corev1.Pod, _ []framework.NodeInfo) (*framework.PreFilterResult, *framework.Status) {
	if l.store.disabled {
		return nil, framework.NewStatus(framework.Skip)
	}

	volumes, err := l.resolveLonghornVolumes(pod)
	if err != nil {
		return nil, framework.AsStatus(err)
	}

	if len(volumes) == 0 {
		klog.Infof("LonghornScheduler: pod %s/%s has no longhorn volumes, skipping", pod.Namespace, pod.Name)
		state.Write(stateKey, &longhornState{})
		return nil, framework.NewStatus(framework.Skip)
	}

	replicaNodes := make(map[string]map[string]bool, len(volumes))
	for _, vol := range volumes {
		replicas, err := l.store.listVolumeReplicas(vol.Name)
		if err != nil {
			return nil, framework.AsStatus(fmt.Errorf("failed to list replicas for volume %s: %w", vol.Name, err))
		}
		nodes := make(map[string]bool)
		for _, r := range replicas {
			if nodeID := getReplicaNodeID(r); nodeID != "" {
				nodes[nodeID] = true
			}
		}
		replicaNodes[vol.Name] = nodes
	}

	allowEmptyNodeSelector, err := l.store.getSettingAsBool(settingAllowEmptyNodeSelector)
	if err != nil {
		return nil, framework.AsStatus(fmt.Errorf("failed to get setting %s: %w", settingAllowEmptyNodeSelector, err))
	}
	allowEmptyDiskSelector, err := l.store.getSettingAsBool(settingAllowEmptyDiskSelector)
	if err != nil {
		return nil, framework.AsStatus(fmt.Errorf("failed to get setting %s: %w", settingAllowEmptyDiskSelector, err))
	}
	overProvisioningPct, err := l.store.getSettingAsInt64(settingStorageOverProvisioningPct)
	if err != nil {
		return nil, framework.AsStatus(fmt.Errorf("failed to get setting %s: %w", settingStorageOverProvisioningPct, err))
	}

	// When a single node already hosts replicas for all volumes, this is probably a pod restart.
	// Restrict scheduling to that node to avoid unnecessary volume replica rebuilds.
	// If the pod has already failed a scheduling attempt (PodScheduled=False),
	// skip this optimization — the original node maybe is not able to pass other filters (e.g. PodAntiAffinity),
	// and restricting to it would block scheduling of this pod indefinitely.
	isPodRestart := false
	originalNode := findOriginalNode(volumes, replicaNodes)
	if originalNode != "" {
		schedulable, _, _, _ := l.isNodeSchedulable(originalNode)
		isPodRestart = schedulable && !hasPodSchedulingFailure(pod)
	}

	state.Write(stateKey, &longhornState{
		volumes:                volumes,
		replicaNodes:           replicaNodes,
		isPodRestart:           isPodRestart,
		originalNode:           originalNode,
		allowEmptyNodeSelector: allowEmptyNodeSelector,
		allowEmptyDiskSelector: allowEmptyDiskSelector,
		overProvisioningPct:    overProvisioningPct,
	})

	klog.Infof("LonghornScheduler: pod %s/%s prefilter state: replicaNodes=%v, originalNode=%q, isPodRestart=%v", pod.Namespace, pod.Name, replicaNodes, originalNode, isPodRestart)

	return nil, framework.NewStatus(framework.Success)
}

func (l *LonghornScheduler) PreFilterExtensions() framework.PreFilterExtensions {
	return nil
}

func findOriginalNode(volumes []volumeInfo, replicaNodes map[string]map[string]bool) string {
	candidates := make(map[string]bool)
	for _, nodeSet := range replicaNodes {
		for nodeName := range nodeSet {
			candidates[nodeName] = true
		}
	}
	for nodeName := range candidates {
		hasAll := true
		for _, vol := range volumes {
			if !replicaNodes[vol.Name][nodeName] {
				hasAll = false
				break
			}
		}
		if hasAll {
			return nodeName
		}
	}
	return ""
}

// Filter checks whether a candidate node can host the pod's Longhorn volumes.
// It verifies the Longhorn node is schedulable, matches volume node selectors,
// and has enough disk capacity to place volumes that don't already have replicas
// on this node. Disk assignment uses backtracking bin packing.
func (l *LonghornScheduler) Filter(ctx context.Context, state framework.CycleState, pod *corev1.Pod, nodeInfo framework.NodeInfo) (result *framework.Status) {
	s, err := state.Read(stateKey)
	if err != nil {
		return framework.AsStatus(err)
	}
	ls := s.(*longhornState)

	nodeName := nodeInfo.Node().Name

	defer func() {
		if result.Code() == framework.Success {
			klog.V(4).Infof("LonghornScheduler: node %s passed filter for pod %s/%s", nodeName, pod.Namespace, pod.Name)
		} else {
			klog.V(4).Infof("LonghornScheduler: node %s rejected for pod %s/%s: %s", nodeName, pod.Namespace, pod.Name, result.Message())
		}
	}()

	if ls.isPodRestart {
		if nodeName == ls.originalNode {
			return framework.NewStatus(framework.Success)
		}
		return framework.NewStatus(framework.Unschedulable, filterReason("pod is restarting and scheduled to its original node"))
	}

	l.reservedMu.RLock()
	_, reserved := l.reservedNodes[nodeName]
	l.reservedMu.RUnlock()
	if reserved {
		return framework.NewStatus(framework.Unschedulable, filterReason("node had been temporarily reserved"))
	}

	schedulable, reason, lhNodeSpec, lhNodeStatus := l.isNodeSchedulable(nodeName)
	if !schedulable {
		return framework.NewStatus(framework.Unschedulable, filterReason(reason))
	}

	var volumesToPlace []volumeInfo
	for _, vol := range ls.volumes {
		if ls.replicaNodes[vol.Name][nodeName] {
			continue
		}
		if vol.Size == 0 {
			continue
		}
		if !isSelectorsInTags(lhNodeSpec.Tags, vol.NodeSelector, ls.allowEmptyNodeSelector) {
			return framework.NewStatus(framework.Unschedulable, filterReason("node did not match volume node selector"))
		}
		volumesToPlace = append(volumesToPlace, vol)
	}

	if len(volumesToPlace) == 0 {
		return framework.NewStatus(framework.Success)
	}

	sort.Slice(volumesToPlace, func(i, j int) bool {
		return volumesToPlace[i].Size > volumesToPlace[j].Size
	})

	if !binPacking(lhNodeSpec, lhNodeStatus, volumesToPlace, ls.allowEmptyDiskSelector, ls.overProvisioningPct) {
		return framework.NewStatus(framework.Unschedulable, filterReason("node did not have enough disk capacity"))
	}
	return framework.NewStatus(framework.Success)
}

func (l *LonghornScheduler) isNodeSchedulable(nodeName string) (bool, string, nodeSpec, nodeStatus) {
	lhNodeObj, err := l.store.getNode(nodeName)
	if err != nil {
		return false, "node could not be fetched", nodeSpec{}, nodeStatus{}
	}

	lhNodeSpec := parseNodeSpec(lhNodeObj)
	lhNodeStatus := parseNodeStatus(lhNodeObj)

	if !lhNodeSpec.AllowScheduling || lhNodeSpec.EvictionRequested {
		return false, "node did not allow scheduling", nodeSpec{}, nodeStatus{}
	}
	if getConditionStatus(lhNodeStatus.Conditions, "Ready") != "True" {
		return false, "node had unready condition", nodeSpec{}, nodeStatus{}
	}
	if getConditionStatus(lhNodeStatus.Conditions, "Schedulable") != "True" {
		return false, "node had unschedulable condition", nodeSpec{}, nodeStatus{}
	}

	return true, "", lhNodeSpec, lhNodeStatus
}

func binPacking(spec nodeSpec, status nodeStatus, volumes []volumeInfo, allowEmptyDiskSelector bool, overProvisioningPct int64) bool {
	diskScheduled := make(map[string]int64)

	var backtrack func(int) bool
	backtrack = func(index int) bool {
		if index == len(volumes) {
			return true
		}

		for diskName, ds := range status.DiskStatus {
			diskSpec, ok := spec.Disks[diskName]
			if !ok || !diskSpec.AllowScheduling {
				continue
			}
			if !isSelectorsInTags(diskSpec.Tags, volumes[index].DiskSelector, allowEmptyDiskSelector) {
				continue
			}

			overProvisionLimit := ((ds.StorageMaximum - diskSpec.StorageReserved) * overProvisioningPct) / 100
			storageSchedulable := overProvisionLimit - ds.StorageScheduled

			if diskScheduled[diskName]+volumes[index].Size <= storageSchedulable {
				diskScheduled[diskName] += volumes[index].Size
				if backtrack(index + 1) {
					return true
				}
				diskScheduled[diskName] -= volumes[index].Size
			}
		}

		return false
	}

	return backtrack(0)
}

func filterReason(msg string) string {
	return msg + " (longhorn)"
}

// resolveLonghornVolumes walks the pod's volumes, finds PVCs backed by
// Longhorn PVs (via CSI driver name), and returns their scheduling metadata.
// Unbound PVCs with a Longhorn StorageClass are also included so that new
// volumes are accounted for during capacity checks.
func (l *LonghornScheduler) resolveLonghornVolumes(pod *corev1.Pod) ([]volumeInfo, error) {
	var volumes []volumeInfo

	for _, podVol := range pod.Spec.Volumes {
		if podVol.PersistentVolumeClaim == nil {
			continue
		}

		pvc, err := l.pvcLister.PersistentVolumeClaims(pod.Namespace).Get(podVol.PersistentVolumeClaim.ClaimName)
		if err != nil {
			return nil, err
		}

		if pvc.Status.Phase != corev1.ClaimBound {
			if l.isLonghornStorageClass(pvc.Spec.StorageClassName) {
				size := int64(0)
				if req, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
					size = req.Value()
				}
				volumes = append(volumes, volumeInfo{
					Name: pvc.Name,
					Size: size,
				})
			}
			continue
		}

		pv, err := l.pvLister.Get(pvc.Spec.VolumeName)
		if err != nil {
			return nil, err
		}
		if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != longhornDriverName {
			continue
		}

		volObj, err := l.store.getVolume(pv.Spec.CSI.VolumeHandle)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, parseVolumeInfo(volObj))
	}

	return volumes, nil
}

func (l *LonghornScheduler) isLonghornStorageClass(scName *string) bool {
	if scName == nil {
		return false
	}
	sc, err := l.scLister.Get(*scName)
	if err != nil {
		return false
	}
	return sc.Provisioner == longhornDriverName
}

func getConditionStatus(conditions []condition, condType string) string {
	for _, c := range conditions {
		if c.Type == condType {
			return c.Status
		}
	}
	return ""
}

func isSelectorsInTags(tags, selectors []string, allowEmpty bool) bool {
	if len(selectors) == 0 {
		return allowEmpty
	}
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}
	for _, s := range selectors {
		if !tagSet[s] {
			return false
		}
	}
	return true
}

// hasPodSchedulingFailure returns true if the pod has already failed at least
// one scheduling attempt (PodScheduled condition is False).
func hasPodSchedulingFailure(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
			return true
		}
	}
	return false
}

// Reserve temporarily blocks a node so that the next scheduling cycle does not place additional volumes on it
// before Longhorn has finished scheduling replicas, which could overprovision the node's storage beyond the limit.
func (l *LonghornScheduler) Reserve(ctx context.Context, state framework.CycleState, pod *corev1.Pod, nodeName string) *framework.Status {
	if l.store.disabled || l.reservationTimeout == 0 {
		return framework.NewStatus(framework.Success)
	}

	s, err := state.Read(stateKey)
	if err != nil {
		return framework.AsStatus(err)
	}
	ls := s.(*longhornState)

	if ls.isPodRestart || len(ls.volumes) == 0 {
		return framework.NewStatus(framework.Success)
	}

	l.reservedMu.Lock()
	l.reservedNodes[nodeName] = time.Now()
	l.reservedMu.Unlock()
	klog.Infof("LonghornScheduler: reserved node %s for pod %s/%s", nodeName, pod.Namespace, pod.Name)

	return framework.NewStatus(framework.Success)
}

// Unreserve releases the node reservation when scheduling fails after Reserve.
func (l *LonghornScheduler) Unreserve(ctx context.Context, state framework.CycleState, pod *corev1.Pod, nodeName string) {
	if l.store.disabled || l.reservationTimeout == 0 {
		return
	}

	s, err := state.Read(stateKey)
	if err != nil {
		return
	}
	ls := s.(*longhornState)

	if ls.isPodRestart || len(ls.volumes) == 0 {
		return
	}

	l.reservedMu.Lock()
	delete(l.reservedNodes, nodeName)
	l.reservedMu.Unlock()
	klog.Infof("LonghornScheduler: unreserved node %s, pod %s/%s scheduling failed", nodeName, pod.Namespace, pod.Name)
}

// PostBind starts an asynchronous goroutine that watches the Longhorn Node CR
// until all expected replicas appear in diskStatus.scheduledReplica, or the
// reservation timeout expires. The node reservation is always released on exit.
func (l *LonghornScheduler) PostBind(ctx context.Context, state framework.CycleState, pod *corev1.Pod, nodeName string) {
	if l.store.disabled || l.reservationTimeout == 0 {
		return
	}

	s, err := state.Read(stateKey)
	if err != nil {
		return
	}
	ls := s.(*longhornState)

	if ls.isPodRestart || len(ls.volumes) == 0 {
		return
	}

	go l.waitForReplicasScheduled(nodeName, pod)
}

// waitForReplicasScheduled watches the Longhorn Node CR until all expected
// replicas for the pod's volumes appear in diskStatus.scheduledReplica.
func (l *LonghornScheduler) waitForReplicasScheduled(nodeName string, pod *corev1.Pod) {
	defer func() {
		l.reservedMu.Lock()
		reservedAt, reserved := l.reservedNodes[nodeName]
		delete(l.reservedNodes, nodeName)
		l.reservedMu.Unlock()
		if reserved {
			duration := time.Since(reservedAt)
			nodeReservationDuration.Observe(duration.Seconds())
			klog.Infof("LonghornScheduler: node %s reserved for %v, pod %s/%s", nodeName, duration, pod.Namespace, pod.Name)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), l.reservationTimeout)
	defer cancel()

	watcher, err := l.store.watchNode(ctx, nodeName)
	if err != nil {
		klog.Warningf("LonghornScheduler: failed to watch node %s: %v", nodeName, err)
		return
	}
	defer watcher.Stop()

	var expectedReplicas map[string]bool

	for {
		select {
		case <-ctx.Done():
			klog.Infof("LonghornScheduler: reservation timeout for node %s, pod %s/%s", nodeName, pod.Namespace, pod.Name)
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return
			}

			nodeObj, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				continue
			}

			if expectedReplicas == nil {
				expectedReplicas = l.resolveExpectedReplicas(pod)
				if expectedReplicas == nil {
					continue
				}
			}

			if allReplicasScheduled(parseNodeStatus(nodeObj), expectedReplicas) {
				klog.Infof("LonghornScheduler: all replicas scheduled on node %s for pod %s/%s", nodeName, pod.Namespace, pod.Name)
				return
			}
		}
	}
}

// resolveExpectedReplicas builds a set of replica names that should appear on
// the reserved node for the pod's Longhorn volumes.
func (l *LonghornScheduler) resolveExpectedReplicas(pod *corev1.Pod) map[string]bool {
	volumes, err := l.resolveLonghornVolumes(pod)
	if err != nil {
		klog.Warningf("LonghornScheduler: failed to resolve volumes for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return nil
	}

	expected := make(map[string]bool)
	for _, vol := range volumes {
		replicas, err := l.store.listVolumeReplicas(vol.Name)
		if err != nil {
			klog.Warningf("LonghornScheduler: failed to list replicas for volume %s: %v", vol.Name, err)
			return nil
		}
		for _, r := range replicas {
			expected[r.GetName()] = true
		}
	}
	if len(expected) == 0 {
		return nil
	}
	return expected
}

// allReplicasScheduled checks if all expected replicas appear in the node's diskStatus["diskName"].ScheduledReplica maps.
func allReplicasScheduled(status nodeStatus, expected map[string]bool) bool {
	found := 0
	for _, ds := range status.DiskStatus {
		for replicaName := range ds.ScheduledReplica {
			if expected[replicaName] {
				found++
			}
		}
	}
	return found == len(expected)
}
