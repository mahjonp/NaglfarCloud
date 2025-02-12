/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// PodGroupSpec defines the desired state of PodGroup
type PodGroupSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// MinMember defines the minimal number of members/tasks to run the pod group;
	// if there's not enough resources to start all tasks, the scheduler
	// will not start anyone.
	MinMember int32 `json:"minMember,omitempty"`

	// Exclusive is a flag to decide should this pod group monopolize nodes
	// +optional
	Exclusive *bool `json:"exclusive,omitempty"`

	// SubGroups is a list of sub pod groups
	// +optional
	SubGroups map[string]PodGroupSpec `json:"subGroups,omitempty"`

	// ScheduleTimeout sets the max wait scheduing time before the podGroup is ready
	// +optional
	ScheduleTimeout *Duration `json:"scheduleTimeout,omitempty"`
}

// PodGroupStatus defines the observed state of PodGroup
type PodGroupStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// RescheduleTime is the reschedule time of this pod group
	// +optional
	RescheduleTimes map[string]metav1.Time `json:"rescheduleTimes,omitempty"`
}

// +kubebuilder:object:root=true

// PodGroup is the Schema for the podgroups API
// +kubebuilder:subresource:status
type PodGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PodGroupSpec   `json:"spec,omitempty"`
	Status PodGroupStatus `json:"status,omitempty"`
}

// IsExclusive is a wrapper of Exclusive field,
// it returns false if Exclusive field is nil.
func (pg *PodGroupSpec) IsExclusive() bool {
	if pg.Exclusive == nil {
		return false
	}
	return *pg.Exclusive
}

func (pg *PodGroupSpec) GetScheduleTimeout() (*time.Duration, error) {
	if pg.ScheduleTimeout == nil {
		return nil, nil
	}
	d, err := pg.ScheduleTimeout.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse scheduleTimeout %s error: %v", *pg.ScheduleTimeout, err)
	}
	return &d, nil
}

// ScheduleTime is a wrapper of RescheduleTime field of status,
// it returns create time if RescheduleTime field is nil.
func (pg *PodGroup) ScheduleTime(pgName string) time.Time {
	if pg.Status.RescheduleTimes == nil {
		return pg.CreationTimestamp.Time
	}
	if _, ok := pg.Status.RescheduleTimes[pgName]; !ok {
		return pg.CreationTimestamp.Time
	}
	return pg.Status.RescheduleTimes[pgName].Time
}

// +kubebuilder:object:root=true

// PodGroupList contains a list of PodGroup
type PodGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PodGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PodGroup{}, &PodGroupList{})
}
