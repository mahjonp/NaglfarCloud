// Copyright 2021 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	informerv1 "k8s.io/client-go/informers/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	framework "k8s.io/kubernetes/pkg/scheduler/framework/v1alpha1"

	apiv1 "github.com/PingCAP-QE/NaglfarCloud/pkg/api/v1"
	"github.com/PingCAP-QE/NaglfarCloud/pkg/client"
)

var errorWaiting = fmt.Errorf("waiting")

func subGroupNotFound(podGroup *apiv1.PodGroup, name string) error {
	return fmt.Errorf("subgroup %s not found in group %s/%s", name, podGroup.Namespace, podGroup.Name)
}

// PodGroupManager is the mananger of podgroup
type PodGroupManager struct {
	ctx context.Context
	// snapshotSharedLister is pod shared list
	snapshotSharedLister framework.SharedLister
	// scheduleTimeout is the default time when group scheduling.
	// If podgroup's ScheduleTimeoutSeconds set, that would be used.
	scheduleTimeout time.Duration
	// podLister is pod lister
	podLister listerv1.PodLister
	// kubeClient is kube clientset
	kubeClientSet clientset.Interface
	// schedulingClient is podGroupClient
	schedulingClient *client.SchedulingClient
}

// NewPodGroupManager is the constructor of PodGroupManager
func NewPodGroupManager(snapshotSharedLister framework.SharedLister,
	scheduleTimeout time.Duration,
	podInformer informerv1.PodInformer,
	kubeClientSet clientset.Interface,
	schedulingClient *client.SchedulingClient) *PodGroupManager {
	return &PodGroupManager{
		ctx:                  context.Background(),
		snapshotSharedLister: snapshotSharedLister,
		scheduleTimeout:      scheduleTimeout,
		podLister:            podInformer.Lister(),
		kubeClientSet:        kubeClientSet,
		schedulingClient:     schedulingClient,
	}
}

// PreFilter filters out a pod if it
// - Check if the numbers of total pods is less than minMenber
func (mgr *PodGroupManager) PreFilter(ctx context.Context, pod *corev1.Pod) error {
	pg, pgSpec, err := mgr.podGroup(pod)
	if err != nil {
		return fmt.Errorf("cannot get pod group: %s", err.Error())
	}
	if pg == nil {
		return nil
	}
	pgn := getPodGroupNameFromPod(pod)
	if pg.ScheduleTime(pgn).After(time.Now()) {
		return fmt.Errorf("podGroup %s/%s denies to schedule util %s", pg.Namespace, pg.Name, pg.ScheduleTime(pgn).String())
	}
	pgns := getPodGroupNameSliceFromPod(pod)
	friends, err := mgr.podLister.Pods(pod.Namespace).List(pgns)

	if err != nil {
		return fmt.Errorf("cannot list pods in the podGroup %s/%s: %s", pg.Namespace, pgns.PodGroupName(), err.Error())
	}
	if len(friends) < int(pgSpec.MinMember) {
		return fmt.Errorf("the number of pods in the podGroup %s/%s is less than minMember: %d < %d", pg.Namespace, pgns.PodGroupName(), len(friends), pgSpec.MinMember)
	}
	return nil
}

// Permit permits a pod to run
func (mgr *PodGroupManager) Permit(
	ctx context.Context,
	pod *corev1.Pod,
	nodeName string,
	defaultTimeout time.Duration) (bool, time.Duration, error) {
	pg, pgSpec, err := mgr.podGroup(pod)
	if err != nil {
		return false, 0, fmt.Errorf("cannot get pod group: %v", err)
	}
	if pg == nil {
		return true, 0, nil
	}

	assigned := mgr.calculateAssignedPods(pod.Namespace, getPodGroupNameSliceFromPod(pod))
	// The number of pods that have been assigned nodes is calculated from the snapshot.
	// The current pod in not included in the snapshot during the current scheduling cycle.
	if assigned+1 < int(pgSpec.MinMember) {
		waitTimeout := defaultTimeout
		if timeout, err := pgSpec.GetScheduleTimeout(); err != nil {
			return false, 0, err
		} else if timeout != nil {
			waitTimeout = *timeout
		}
		return false, waitTimeout, errorWaiting
	}
	return true, 0, nil
}

// podGroup returns the super pod group and sub pod group that a Pod belongs to.
func (mgr *PodGroupManager) podGroup(pod *corev1.Pod) (*apiv1.PodGroup, *apiv1.PodGroupSpec, error) {
	names := getPodGroupNameSliceFromPod(pod)
	if len(names) == 0 {
		return nil, nil, nil
	}

	podGroup, err := mgr.schedulingClient.PodGroups(pod.Namespace).Get(mgr.ctx, names[0], metav1.GetOptions{})
	if err != nil {
		return nil, nil, err
	}

	pgSpec := &podGroup.Spec
	for _, name := range names[1:] {
		if pgSpec.SubGroups == nil {
			return nil, nil, subGroupNotFound(podGroup, name)
		}
		spec, ok := pgSpec.SubGroups[name]
		if !ok {
			return nil, nil, subGroupNotFound(podGroup, name)
		}

		if spec.Exclusive == nil && pgSpec.Exclusive != nil {
			// inherit exclusive from super group
			spec.Exclusive = pgSpec.Exclusive
		}
		if spec.ScheduleTimeout == nil && pgSpec.ScheduleTimeout != nil {
			// inherit scheduleTimeout from super group
			spec.ScheduleTimeout = pgSpec.ScheduleTimeout
		}
		pgSpec = &spec
	}

	return podGroup, pgSpec, nil
}

func (mgr *PodGroupManager) calculateAssignedPods(namespace string, pgNameSlice PodGroupNameSlice) int {
	nodeInfos, err := mgr.snapshotSharedLister.NodeInfos().List()
	if err != nil {
		klog.Errorf("cannot get nodeInfos from frameworkHandle: %v", err)
		return 0
	}
	var count int
	for _, nodeInfo := range nodeInfos {
		for _, podInfo := range nodeInfo.Pods {
			pod := podInfo.Pod
			pgNameS := getPodGroupNameSliceFromPod(pod)
			if pod.Namespace == namespace && pgNameS.IsBelongTo(pgNameSlice) &&
				pod.Spec.NodeName != "" {
				count++
			}
		}
	}

	return count
}

// reschedule is a method to reschedule pod group to end of queue.
func (mgr *PodGroupManager) reschedule(podGroup *apiv1.PodGroup, pgName string, rescheduleDelayOffset time.Duration) error {
	if podGroup.Status.RescheduleTimes == nil {
		podGroup.Status.RescheduleTimes = make(map[string]metav1.Time)
	}
	if _, ok := podGroup.Status.RescheduleTimes[pgName]; !ok {
		podGroup.Status.RescheduleTimes[pgName] = metav1.Time{}
	}
	now := time.Now()
	if podGroup.Status.RescheduleTimes[pgName].Time.Before(now) {
		podGroup.Status.RescheduleTimes[pgName] = metav1.Time{Time: now.Add(rescheduleDelayOffset)}
		if _, err := mgr.schedulingClient.PodGroups(podGroup.Namespace).UpdateStatus(mgr.ctx, podGroup, metav1.UpdateOptions{}); err != nil {
			return err
		}
		klog.V(2).Infof("Reschedule podGroup %s/%s on %s", podGroup.Namespace, podGroup.Name, podGroup.Status.RescheduleTimes[pgName].Time.String())
	}
	return nil
}

func (mgr *PodGroupManager) getScheduleTime(pod *corev1.Pod, defaultTime time.Time) time.Time {
	podGroup, _, _ := mgr.podGroup(pod)
	if podGroup == nil {
		return defaultTime
	}

	return podGroup.ScheduleTime(getPodGroupNameFromPod(pod))
}

// getPodGroupNameFromPod is a function to get podgroup label of pod
func getPodGroupNameFromPod(pod *corev1.Pod) string {
	return strings.TrimSpace(pod.Labels[PodGroupLabel])
}

// getPodGroupNameSliceFromPod is a function to split podgroup by dot
// return nil if the podgroup label is not set
func getPodGroupNameSliceFromPod(pod *corev1.Pod) PodGroupNameSlice {
	path := getPodGroupNameFromPod(pod)
	if path == "" {
		return nil
	}
	return strings.Split(path, subGroupSeparator)
}

func isControlledByDaemonSet(pod *corev1.Pod) bool {
	controller := metav1.GetControllerOf(pod)
	if controller == nil {
		return false
	}
	return controller.Kind == "DaemonSet"
}
