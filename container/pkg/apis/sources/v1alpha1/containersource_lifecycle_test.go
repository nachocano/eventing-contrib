/*
Copyright 2020 The Knative Authors

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

package v1alpha1

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

var (
	availableDeployment = &appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	unavailableDeployment = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "unavailable",
		},
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}
)

func TestContainerSourceStatusIsReady(t *testing.T) {
	sink, _ := apis.ParseURL("uri://example")

	tests := []struct {
		name string
		s    *ContainerSourceStatus
		want bool
	}{{
		name: "uninitialized",
		s:    &ContainerSourceStatus{},
		want: false,
	}, {
		name: "initialized",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			return s
		}(),
		want: false,
	}, {
		name: "mark deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.PropagateDeploymentAvailability(availableDeployment)
			return s
		}(),
		want: false,
	}, {
		name: "mark sink",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			return s
		}(),
		want: false,
	}, {
		name: "mark sink and deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			s.PropagateDeploymentAvailability(availableDeployment)
			return s
		}(),
		want: true,
	}, {
		name: "mark sink and deployed then no sink",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			s.PropagateDeploymentAvailability(availableDeployment)
			s.MarkNoSink("Testing", "")
			return s
		}(),
		want: false,
	}, {
		name: "mark sink and deployed then not deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			s.PropagateDeploymentAvailability(availableDeployment)
			s.PropagateDeploymentAvailability(unavailableDeployment)
			return s
		}(),
		want: false,
	}, {
		name: "mark sink empty and deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(nil)
			s.PropagateDeploymentAvailability(availableDeployment)
			return s
		}(),
		want: false,
	}, {
		name: "mark sink empty and deployed then sink",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(nil)
			s.PropagateDeploymentAvailability(availableDeployment)
			s.MarkSink(sink)
			return s
		}(),
		want: true,
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.s.IsReady()
			if diff := cmp.Diff(test.want, got); diff != "" {
				t.Errorf("%s: unexpected condition (-want, +got) = %v", test.name, diff)
			}
		})
	}
}

func TestContainerSourceStatusGetCondition(t *testing.T) {
	sink, _ := apis.ParseURL("uri://example")

	tests := []struct {
		name      string
		s         *ContainerSourceStatus
		condQuery apis.ConditionType
		want      *apis.Condition
	}{{
		name:      "uninitialized",
		s:         &ContainerSourceStatus{},
		condQuery: ContainerConditionReady,
		want:      nil,
	}, {
		name: "initialized",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:   ContainerConditionReady,
			Status: corev1.ConditionUnknown,
		},
	}, {
		name: "mark deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.PropagateDeploymentAvailability(availableDeployment)
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:   ContainerConditionReady,
			Status: corev1.ConditionUnknown,
		},
	}, {
		name: "mark sink",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:   ContainerConditionReady,
			Status: corev1.ConditionUnknown,
		},
	}, {
		name: "mark sink and deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			s.PropagateDeploymentAvailability(availableDeployment)
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:   ContainerConditionReady,
			Status: corev1.ConditionTrue,
		},
	}, {
		name: "mark sink and deployed then no sink",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			s.PropagateDeploymentAvailability(availableDeployment)
			s.MarkNoSink("Testing", "hi%s", "")
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:    ContainerConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  "Testing",
			Message: "hi",
		},
	}, {
		name: "mark sink and deployed then not deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(sink)
			s.PropagateDeploymentAvailability(availableDeployment)
			s.PropagateDeploymentAvailability(unavailableDeployment)
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:    ContainerConditionReady,
			Status:  corev1.ConditionFalse,
			Message: "unavailable",
		},
	}, {
		name: "mark sink empty and deployed",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(nil)
			s.PropagateDeploymentAvailability(availableDeployment)
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:    ContainerConditionReady,
			Status:  corev1.ConditionFalse,
			Reason:  "SinkEmpty",
			Message: "Sink has resolved to empty.",
		},
	}, {
		name: "mark sink empty and deployed then sink",
		s: func() *ContainerSourceStatus {
			s := &ContainerSourceStatus{}
			s.InitializeConditions()
			s.MarkSink(nil)
			s.PropagateDeploymentAvailability(availableDeployment)
			s.MarkSink(sink)
			return s
		}(),
		condQuery: ContainerConditionReady,
		want: &apis.Condition{
			Type:   ContainerConditionReady,
			Status: corev1.ConditionTrue,
		},
	}}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := test.s.GetCondition(test.condQuery)
			ignoreTime := cmpopts.IgnoreFields(apis.Condition{},
				"LastTransitionTime", "Severity")
			if diff := cmp.Diff(test.want, got, ignoreTime); diff != "" {
				t.Errorf("unexpected condition (-want, +got) = %v", diff)
			}
		})
	}
}
