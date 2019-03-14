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

package crd

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/util/validation"
	"regexp"
	"strings"

	"github.com/knative/eventing-sources/pkg/controller/sdk"
	eventingv1alpha1 "github.com/knative/eventing/pkg/apis/eventing/v1alpha1"
	"github.com/knative/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "knative-sources-crd-controller"

	// Label to get namespaces with knative-eventing enabled.
	knativeEventingLabelKey   = "knative-eventing-injection"
	knativeEventingLabelValue = "enabled"

	// Label to get eventing sources.
	eventingSourceLabelKey   = "eventing.knative.dev/source"
	eventingSourceLabelValue = "true"

	// Label to get the event types generated by a particular CRD source.
	crdEventTypeLabelKey = "eventing.knative.dev/eventtype"

	eventTypesCreated      = "EventTypesCreated"
	eventTypesCreateFailed = "EventTypesCreateFailed"
)

var (
	// Only allow alphanumeric, '-' or '.'.
	validChars = regexp.MustCompile(`[^-\.a-z0-9]+`)
)

// Add creates a new CRD Controller and adds it to the
// Manager with default RBAC. The Manager will set fields on the
// Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {

	p := &sdk.Provider{
		AgentName: controllerAgentName,
		Parent:    &v1beta1.CustomResourceDefinition{},
		Owns:      []runtime.Object{&corev1.Namespace{}},
		Reconciler: &reconciler{
			recorder: mgr.GetRecorder(controllerAgentName),
		},
	}

	return p.Add(mgr)
}

type reconciler struct {
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
}

func (r *reconciler) Reconcile(ctx context.Context, object runtime.Object) error {
	logger := logging.FromContext(ctx)

	crd, ok := object.(*v1beta1.CustomResourceDefinition)
	if !ok {
		logger.Errorf("could not find crd %v", object)
		return nil
	}

	// See if the CRD has been deleted.
	accessor, err := meta.Accessor(crd)
	if err != nil {
		logger.Warnf("Failed to get metadata accessor: %s", zap.Error(err))
		return err
	}

	var reconcileErr error
	if accessor.GetDeletionTimestamp() == nil {
		reconcileErr = r.reconcile(ctx, crd)
	} else {
		reconcileErr = r.finalize(ctx, crd)
	}
	return reconcileErr
}

func (r *reconciler) reconcile(ctx context.Context, crd *v1beta1.CustomResourceDefinition) error {
	logger := logging.FromContext(ctx)

	if crd.Labels[eventingSourceLabelKey] != eventingSourceLabelValue {
		logger.Debugf("Not reconciling CRD %s", crd.Name)
		return nil
	}

	logger.Infof("Reconciling CRD %s", crd.Name)

	err := r.reconcileNamespaces(ctx, crd)
	if err != nil {
		logger.Errorf("Error reconciling namespaces, %v", err)
		return err
	}

	return nil
}

func (r *reconciler) reconcileNamespaces(ctx context.Context, crd *v1beta1.CustomResourceDefinition) error {
	logger := logging.FromContext(ctx)
	eventingNamespaces, err := r.getEventingNamespaces(ctx, crd)
	if err != nil {
		return err
	}

	for _, eventingNamespace := range eventingNamespaces {
		err := r.reconcileEventTypes(ctx, crd, eventingNamespace)
		if err != nil {
			logger.Errorf("Error reconciling EventTypes for namespace %s", eventingNamespace.Name)
			return err
		}
		logger.Infof("Reconciled EventTypes for namespace %s", eventingNamespace.Name)
	}
	return nil
}

func (r *reconciler) reconcileEventTypes(ctx context.Context, crd *v1beta1.CustomResourceDefinition, namespace corev1.Namespace) error {
	current, err := r.getEventTypes(ctx, crd, namespace)
	if err != nil {
		return err
	}
	expected := r.newEventTypes(crd, namespace)
	diff := difference(current, expected)
	if len(diff) > 0 {
		// TODO bulk creation, need to +genclient the EventList struct?
		for _, eventType := range diff {
			err = r.client.Create(ctx, &eventType)
			if err != nil {
				r.recorder.Eventf(crd, corev1.EventTypeWarning, eventTypesCreateFailed, "Could not create event type %q", eventType.Spec.Type)
				return err
			}
		}
		r.recorder.Eventf(crd, corev1.EventTypeNormal, eventTypesCreated, "Event types created, total %d", len(diff))
	}
	return nil
}

func (r *reconciler) getEventTypes(ctx context.Context, crd *v1beta1.CustomResourceDefinition, namespace corev1.Namespace) ([]eventingv1alpha1.EventType, error) {
	eventTypes := make([]eventingv1alpha1.EventType, 0)

	opts := &client.ListOptions{
		Namespace:     namespace.Name,
		LabelSelector: labels.SelectorFromSet(eventTypesLabels(crd.Name)),
		// Set Raw because if we need to get more than one page, then we will put the continue token
		// into opts.Raw.Continue.
		Raw: &metav1.ListOptions{},
	}
	for {
		el := &eventingv1alpha1.EventTypeList{}
		if err := r.client.List(ctx, opts, el); err != nil {
			return nil, err
		}

		for _, e := range el.Items {
			eventTypes = append(eventTypes, e)
		}
		if el.Continue != "" {
			opts.Raw.Continue = el.Continue
		} else {
			return eventTypes, nil
		}
	}
}

func (r *reconciler) newEventTypes(crd *v1beta1.CustomResourceDefinition, namespace corev1.Namespace) []eventingv1alpha1.EventType {
	eventTypes := make([]eventingv1alpha1.EventType, 0)
	if crd.Spec.Validation != nil && crd.Spec.Validation.OpenAPIV3Schema != nil {
		properties := crd.Spec.Validation.OpenAPIV3Schema.Properties
		if spec, ok := properties["spec"]; ok {
			// TODO what about the sources that do not specify eventTypes.
			// Might need to standarize the sources fields.
			if eTypes, ok := spec.Properties["eventTypes"]; ok {
				if eTypes.Items != nil && eTypes.Items.Schema != nil {
					for _, eType := range eTypes.Items.Schema.Enum {
						// Remove quotes.
						et := strings.Trim(string(eType.Raw), `"`)
						eventType := eventingv1alpha1.EventType{
							ObjectMeta: metav1.ObjectMeta{
								GenerateName: fmt.Sprintf("%s-", toValidIdentifier(et)),
								Labels:       eventTypesLabels(crd.Name),
								Namespace:    namespace.Name,
							},
							Spec: eventingv1alpha1.EventTypeSpec{
								Type:   et,
								Origin: crd.Name,
								// TODO schema in the CRD?
								Schema: "",
							},
						}
						eventTypes = append(eventTypes, eventType)
					}
				}
			}
		}
	}
	return eventTypes
}

func difference(current []eventingv1alpha1.EventType, expected []eventingv1alpha1.EventType) []eventingv1alpha1.EventType {
	// TODO make a more efficient implementation.
	difference := make([]eventingv1alpha1.EventType, 0)
	for _, e := range expected {
		found := false
		for _, c := range current {
			if e.Spec.Type == c.Spec.Type &&
				e.Spec.Origin == c.Spec.Origin &&
				e.Spec.Schema == c.Spec.Schema {
				found = true
				break
			}
		}
		if !found {
			difference = append(difference, e)
		}
	}
	return difference
}

func (r *reconciler) getEventingNamespaces(ctx context.Context, crd *v1beta1.CustomResourceDefinition) ([]corev1.Namespace, error) {
	sourcesNamespaces := make([]corev1.Namespace, 0)

	opts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(namespaceLabels()),
		// Set Raw because if we need to get more than one page, then we will put the continue token
		// into opts.Raw.Continue.
		Raw: &metav1.ListOptions{},
	}
	for {
		nl := &corev1.NamespaceList{}
		if err := r.client.List(ctx, opts, nl); err != nil {
			return nil, err
		}

		for _, e := range nl.Items {
			sourcesNamespaces = append(sourcesNamespaces, e)
		}
		if nl.Continue != "" {
			opts.Raw.Continue = nl.Continue
		} else {
			return sourcesNamespaces, nil
		}
	}
}

func namespaceLabels() map[string]string {
	return map[string]string{
		knativeEventingLabelKey: knativeEventingLabelValue,
	}
}

func eventTypesLabels(crdName string) map[string]string {
	return map[string]string{
		crdEventTypeLabelKey: crdName,
	}
}

func toValidIdentifier(eventType string) string {
	if msgs := validation.IsDNS1123Subdomain(eventType); len(msgs) != 0 {
		// If it is not a valid DNS1123 name, make it a valid one.
		// TODO take care of size < 63, and starting and end indexes should be alphanumeric.
		eventType = strings.ToLower(eventType)
		eventType = validChars.ReplaceAllString(eventType, "")
	}
	return eventType
}

func (r *reconciler) finalize(ctx context.Context, crd *v1beta1.CustomResourceDefinition) error {
	// should look at all the namespaces with knative enabled
	// and remove the eventtypes from there
	// eventing.knative.dev/source=true
	return nil
}

func (r *reconciler) InjectClient(c client.Client) error {
	r.client = c
	return nil
}
