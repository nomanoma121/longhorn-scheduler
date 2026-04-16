# longhorn-scheduler

A Kubernetes scheduler plugin for co-locating pods with their Longhorn volumes.

## Motivation

We initially relied on Kubernetes native
[storage capacity tracking](https://kubernetes.io/docs/concepts/storage/storage-capacity/)
to co-locate pods and Longhorn volumes on the same node, using volumes with `best-effort` data locality. Over time we ran
into several problems that this mechanism cannot solve:

**1. Pod restart causes replica rebuild**  
When a pod restarts, the scheduler does not guarantee placement on the same node. This causes Longhorn to rebuild volume
replicas on the new node. When many pods restart simultaneously, the resulting rebuild I/O degrades overall cluster
performance. PV node affinity is not used as a workaround because it is immutable in Kubernetes <= 1.34, which would
block volume rebuilds when a node is drained.

**2. Node drain ignores actual disk capacity**  
When a pod is rescheduled after a node drain,
[storage capacity tracking](https://kubernetes.io/docs/concepts/storage/storage-capacity/)
does not apply because the PVCs are already bound. The scheduler has no storage awareness when selecting a new node and
may place the pod on a node without enough disk space.

**3. Multiple PVCs per pod**  
The scheduler evaluates each PVC independently without considering whether a node's disks can collectively accommodate
all of the pod's volumes. This may result in some volumes not having local replicas.

**4. Race condition between kube-scheduler and external-provisioner**  
Node storage capacity data is managed by external-provisioner, not by kube-scheduler itself. When many pods are created
simultaneously, the scheduler may act on stale capacity figures, so
[storage capacity tracking](https://kubernetes.io/docs/concepts/storage/storage-capacity/)
cannot guarantee that every pod lands on a node with enough capacity.

## Features

- **Capacity-aware**: reads Longhorn Node CRs directly to evaluate real available disk capacity
- **Restart-aware**: on pod restart, restricts scheduling to the original node when it is still healthy, preventing
  unnecessary rebuilds
- **Multi-PVC coordination**: considers all Longhorn volumes attached to a pod together when evaluating a node
- **Race condition prevention**: temporarily reserves a node after scheduling to prevent subsequent pods from making
  decisions based on stale capacity data. Effective only for single-replica volumes; node reservation does not
  eliminate the race condition for volumes with multiple replicas.

## Requirements

| Component  | Version              |
|------------|----------------------|
| Kubernetes | >= 1.31              |
| Longhorn   | >= 1.7 (v1beta2 API) |

## Installation

```bash
helm install longhorn-scheduler ./helm
```

## Usage

Set `schedulerName` on any pod or PodTemplate that uses a Longhorn volume:

```yaml
spec:
  schedulerName: longhorn-scheduler
```

## How It Works

**Normal scheduling:**
1. `PreFilter` resolves all PVCs to Longhorn Volume CRs, builds a map of which nodes already have replicas, and reads
   cluster settings
2. `Filter` rejects nodes where the Longhorn node is not schedulable, does not match node or disk selectors, or cannot
   fit all volumes across its disks
3. `Reserve` marks the selected node as temporarily reserved (optional, disabled when `nodeReservationTimeout` is set to 0)
4. `PostBind` clears the reservation once the Longhorn Node CR is updated to include the volume replicas (optional,
   disabled when `nodeReservationTimeout` is set to 0)

**Pod restart (replicas already exist on one node):**
1. `PreFilter` detects that a single node has at least one replica for each volume of the pod, and that node is still healthy
2. `Filter` passes only that node; all others are rejected immediately

## Limitations

- Requires Longhorn v1beta2 API, not compatible with Longhorn < 1.7
- Restart locality is best-effort; if the original node is unhealthy or no longer satisfies pod affinity requirements,  
  the pod is allowed to schedule elsewhere
- Node reservation is only effective for single-replica volumes; for volumes with multiple replicas, the scheduler cannot
  predict where each replica will be placed, so disk capacity may be overcommitted

## License
This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

```
longhorn-scheduler

Copyright (c) 2026-present NAVER Corp.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
       http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```
