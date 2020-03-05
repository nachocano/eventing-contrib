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

package resources

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/eventing-contrib/container/pkg/apis/sources/v1alpha1"
	"knative.dev/pkg/kmeta"
)

const (
	sourceLabelKey = "sources.knative.dev/containerSource"
)

type ContainerArguments struct {
	Source      *v1alpha1.ContainerSource
	Name        string
	Namespace   string
	Template    *corev1.PodTemplateSpec
	Sink        string
	Annotations map[string]string
	Labels      map[string]string
}

func MakeDeployment(src *v1alpha1.ContainerSource) *appsv1.Deployment {
	containers := make([]corev1.Container, 0)
	for _, c := range src.Spec.Template.Spec.Containers {
		c.Env = append(c.Env, corev1.EnvVar{Name: "K_SINK", Value: src.Status.SinkURI.URL().String()})
		containers = append(containers, c)
	}
	src.Spec.Template.Spec.Containers = containers

	deploy := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      GenerateName(src),
			Namespace: src.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*kmeta.NewControllerRef(src),
			},
			Labels: map[string]string{
				sourceLabelKey: src.Name,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					sourceLabelKey: src.Name,
				},
			},
			Template: *template,
		},
	}

	deploy.Spec.Template.Annotations = src.Annotations
	// Then wire through any labels from the source. Do not allow to override
	// our source name. This seems like it would be way errorprone by allowing
	// the matchlabels then to not match, or we'd have to force them to match, etc.
	// just don't allow it.
	for k, v := range src.Labels {
		if k != sourceLabelKey {
			deploy.Spec.Template.Labels[k] = v
		}
	}
	return deploy
}
