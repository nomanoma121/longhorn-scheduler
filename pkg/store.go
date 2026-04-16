// longhorn-scheduler
// Copyright (c) 2026-present NAVER Corp.
// Apache-2.0

package pkg

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const longhornAPIVersion = "v1beta2"

var (
	volumeGVR  = schema.GroupVersionResource{Group: "longhorn.io", Version: longhornAPIVersion, Resource: "volumes"}
	replicaGVR = schema.GroupVersionResource{Group: "longhorn.io", Version: longhornAPIVersion, Resource: "replicas"}
	nodeGVR    = schema.GroupVersionResource{Group: "longhorn.io", Version: longhornAPIVersion, Resource: "nodes"}
	settingGVR = schema.GroupVersionResource{Group: "longhorn.io", Version: longhornAPIVersion, Resource: "settings"}
)

type longhornStore struct {
	client        dynamic.Interface
	volumeLister  cache.GenericLister
	replicaLister cache.GenericLister
	nodeLister    cache.GenericLister
	settingLister cache.GenericLister
	namespace     string
	disabled      bool
}

func newLonghornStore(kubeConfig *rest.Config, namespace string) (*longhornStore, error) {
	client, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	factory := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
		client, 30*time.Second, namespace, nil,
	)

	store := &longhornStore{
		client:        client,
		volumeLister:  factory.ForResource(volumeGVR).Lister(),
		replicaLister: factory.ForResource(replicaGVR).Lister(),
		nodeLister:    factory.ForResource(nodeGVR).Lister(),
		settingLister: factory.ForResource(settingGVR).Lister(),
		namespace:     namespace,
	}

	store.disabled = true

	stopCh := make(chan struct{})
	factory.Start(stopCh)

	go func() {
		factory.WaitForCacheSync(stopCh)
		klog.Infof("Longhorn informers synced, plugin is now active")
		store.disabled = false
	}()

	return store, nil
}

func (s *longhornStore) getVolume(name string) (*unstructured.Unstructured, error) {
	obj, err := s.volumeLister.ByNamespace(s.namespace).Get(name)
	if err != nil {
		return nil, err
	}
	return obj.(*unstructured.Unstructured), nil
}

func (s *longhornStore) getNode(name string) (*unstructured.Unstructured, error) {
	obj, err := s.nodeLister.ByNamespace(s.namespace).Get(name)
	if err != nil {
		return nil, err
	}
	return obj.(*unstructured.Unstructured), nil
}

func (s *longhornStore) listVolumeReplicas(volumeName string) ([]*unstructured.Unstructured, error) {
	selector := labels.SelectorFromSet(labels.Set{"longhornvolume": volumeName})
	objs, err := s.replicaLister.ByNamespace(s.namespace).List(selector)
	if err != nil {
		return nil, err
	}
	result := make([]*unstructured.Unstructured, 0, len(objs))
	for _, obj := range objs {
		result = append(result, obj.(*unstructured.Unstructured))
	}
	return result, nil
}

func (s *longhornStore) getSettingValue(name string) (string, error) {
	obj, err := s.settingLister.ByNamespace(s.namespace).Get(name)
	if err != nil {
		return "", err
	}
	u := obj.(*unstructured.Unstructured)
	val := nested(unstructured.NestedString, u.Object, "value")
	return val, nil
}

func (s *longhornStore) getSettingAsBool(name string) (bool, error) {
	val, err := s.getSettingValue(name)
	if err != nil {
		return false, err
	}
	return val == "true", nil
}

func (s *longhornStore) getSettingAsInt64(name string) (int64, error) {
	val, err := s.getSettingValue(name)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(val, 10, 64)
}

func (s *longhornStore) watchNode(ctx context.Context, name string) (watch.Interface, error) {
	return s.client.Resource(nodeGVR).Namespace(s.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", name),
	})
}

type volumeInfo struct {
	Name         string
	Size         int64
	NodeSelector []string
	DiskSelector []string
}

type nodeSpec struct {
	AllowScheduling   bool
	EvictionRequested bool
	Tags              []string
	Disks             map[string]diskSpec
}

type diskSpec struct {
	AllowScheduling bool
	StorageReserved int64
	Tags            []string
}

type nodeStatus struct {
	Conditions []condition
	DiskStatus map[string]diskStatus
}

type condition struct {
	Type   string
	Status string
}

type diskStatus struct {
	StorageMaximum   int64
	StorageScheduled int64
	ScheduledReplica map[string]bool
}

// nested is a generic wrapper around unstructured.Nested* helpers that logs
// missing or errored fields. Missing or wrong-typed fields yield Go zero
// values, which is safe because the Filter logic treats zero as "not set".
func nested[T any](fn func(map[string]interface{}, ...string) (T, bool, error), obj map[string]interface{}, fields ...string) T {
	val, found, err := fn(obj, fields...)
	path := strings.Join(fields, ".")
	if err != nil {
		klog.Warningf("failed to parse %q from %v: %v", path, obj, err)
	} else if !found {
		klog.Warningf("field %q not found in %v", path, obj)
	}
	return val
}

// parseNodeSpec extracts scheduling-relevant fields from a Longhorn Node CR's spec.
func parseNodeSpec(nodeObj *unstructured.Unstructured) nodeSpec {
	result := nodeSpec{
		AllowScheduling:   nested(unstructured.NestedBool, nodeObj.Object, "spec", "allowScheduling"),
		EvictionRequested: nested(unstructured.NestedBool, nodeObj.Object, "spec", "evictionRequested"),
		Tags:              nested(unstructured.NestedStringSlice, nodeObj.Object, "spec", "tags"),
		Disks:             make(map[string]diskSpec),
	}

	disksMap := nested(unstructured.NestedMap, nodeObj.Object, "spec", "disks")
	for diskName, rawDisk := range disksMap {
		diskMap, _ := rawDisk.(map[string]interface{})
		result.Disks[diskName] = diskSpec{
			AllowScheduling: nested(unstructured.NestedBool, diskMap, "allowScheduling"),
			StorageReserved: nested(unstructured.NestedInt64, diskMap, "storageReserved"),
			Tags:            nested(unstructured.NestedStringSlice, diskMap, "tags"),
		}
	}
	return result
}

// parseNodeStatus extracts conditions and disk capacity from a Longhorn Node CR's status.
func parseNodeStatus(nodeObj *unstructured.Unstructured) nodeStatus {
	result := nodeStatus{
		DiskStatus: make(map[string]diskStatus),
	}

	for _, rawCond := range nested(unstructured.NestedSlice, nodeObj.Object, "status", "conditions") {
		condMap, _ := rawCond.(map[string]interface{})
		result.Conditions = append(result.Conditions, condition{
			Type:   nested(unstructured.NestedString, condMap, "type"),
			Status: nested(unstructured.NestedString, condMap, "status"),
		})
	}

	for diskName, rawDiskStatus := range nested(unstructured.NestedMap, nodeObj.Object, "status", "diskStatus") {
		dsMap, _ := rawDiskStatus.(map[string]interface{})
		scheduledReplicaMap := nested(unstructured.NestedMap, dsMap, "scheduledReplica")
		var scheduledReplicas map[string]bool
		if len(scheduledReplicaMap) > 0 {
			scheduledReplicas = make(map[string]bool, len(scheduledReplicaMap))
			for replicaName := range scheduledReplicaMap {
				scheduledReplicas[replicaName] = true
			}
		}
		result.DiskStatus[diskName] = diskStatus{
			StorageMaximum:   nested(unstructured.NestedInt64, dsMap, "storageMaximum"),
			StorageScheduled: nested(unstructured.NestedInt64, dsMap, "storageScheduled"),
			ScheduledReplica: scheduledReplicas,
		}
	}
	return result
}

// parseVolumeInfo extracts name, size, and selectors from a Longhorn Volume CR.
func parseVolumeInfo(volumeObj *unstructured.Unstructured) volumeInfo {
	volumeNodeSelector := nested(unstructured.NestedStringSlice, volumeObj.Object, "spec", "nodeSelector")
	volumeDiskSelector := nested(unstructured.NestedStringSlice, volumeObj.Object, "spec", "diskSelector")
	volumeSizeStr := nested(unstructured.NestedString, volumeObj.Object, "spec", "size")
	volumeSize, _ := strconv.ParseInt(volumeSizeStr, 10, 64)
	return volumeInfo{
		Name:         volumeObj.GetName(),
		Size:         volumeSize,
		NodeSelector: volumeNodeSelector,
		DiskSelector: volumeDiskSelector,
	}
}

// getReplicaNodeID extracts the node ID from a Longhorn Replica CR.
func getReplicaNodeID(replicaObj *unstructured.Unstructured) string {
	return nested(unstructured.NestedString, replicaObj.Object, "spec", "nodeID")
}
