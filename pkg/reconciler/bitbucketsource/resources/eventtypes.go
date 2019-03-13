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

package resources

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	sourcesv1alpha1 "github.com/knative/eventing-sources/pkg/apis/sources/v1alpha1"
	"github.com/knative/eventing-sources/pkg/bitbucket/utils"
	eventingv1alpha1 "github.com/knative/eventing/pkg/apis/eventing/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	bitBucketOrigin = "bitbucket.org"
)

var (
	// only allow alphanumeric, '-' or '.'.
	validChars = regexp.MustCompile(`[^-\.a-z0-9]+`)
)

// MakeEventTypes generates, but does not create, EventTypes for the given BitBucketSource.
func MakeEventTypes(source *sourcesv1alpha1.BitBucketSource) []eventingv1alpha1.EventType {
	eventTypes := make([]eventingv1alpha1.EventType, 0, len(source.Spec.EventTypes))
	for _, e := range source.Spec.EventTypes {
		cloudEventType := utils.GetCloudEventType(e)
		eventType := eventingv1alpha1.EventType{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: fmt.Sprintf("%s-", toValidIdentifier(cloudEventType)),
				Labels:       utils.EventTypesLabels(),
			},
			Spec: eventingv1alpha1.EventTypeSpec{
				Type:   cloudEventType,
				Origin: bitBucketOrigin,
				// TODO populate the schema.
				Schema: "",
			},
		}
		eventTypes = append(eventTypes, eventType)
	}
	return eventTypes
}

func toValidIdentifier(cloudEventType string) string {
	if msgs := validation.IsDNS1123Subdomain(cloudEventType); len(msgs) != 0 {
		// If it is not a valid DNS1123 name, make it a valid one.
		// TODO take care of size < 63, and starting and end indexes should be alphanumeric.
		cloudEventType = strings.ToLower(cloudEventType)
		cloudEventType = validChars.ReplaceAllString(cloudEventType, "-")
	}
	return cloudEventType
}
