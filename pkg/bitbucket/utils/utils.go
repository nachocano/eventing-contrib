/*
Copyright 2019 The Knative Authors

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

package utils

import (
	"fmt"

	sourcesv1alpha1 "github.com/knative/eventing-sources/pkg/apis/sources/v1alpha1"
)

func GetCloudEventType(eventType string) string {
	return fmt.Sprintf("%s.%s", sourcesv1alpha1.BitBucketSourceEventPrefix, eventType)
}

func EventTypesLabels() map[string]string {
	return map[string]string{
		"eventing.knative.dev/eventtypes": "bitbucket",
	}
}
