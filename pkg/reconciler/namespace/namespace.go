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

package namespace

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/knative/eventing-sources/pkg/controller/sdk"
	"github.com/knative/eventing-sources/pkg/reconciler/namespace/resources"
	eventingv1alpha1 "github.com/knative/eventing/pkg/apis/eventing/v1alpha1"
	"github.com/knative/pkg/logging"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/manager"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "knative-sources-namespace-controller"

	// Label to get namespaces with knative-eventing enabled.
	knativeEventingLabelKey   = "knative-eventing-injection"
	knativeEventingLabelValue = "enabled"

	// Label to get eventing sources.
	eventingSourceLabelKey   = "eventing.knative.dev/source"
	eventingSourceLabelValue = "true"

	// Label to get the event types generated by a particular object.
	eventingEventTypeLabelKey = "eventing.knative.dev/eventtype"

	eventTypesCreated      = "EventTypesCreated"
	eventTypesCreateFailed = "EventTypesCreateFailed"
)

var (
	// TODO make these constants and accessible from different places (e.g., adaptors)
	// This is needed because these sources do not explicitly define the eventTypes they
	// can produce in their CRDs. We may need to try to "standarize" CRDs specs for sources.
	// Question: Why are we changing the eventType for some but not all sources?
	// E.g., for github we do, but for gcp we don't. Even more, for AWS SQS we change it but
	// differently than how we do it in github.
	eventTypeFromCrd = map[string]string{
		"awssqssources.sources.eventing.knative.dev":          "aws.sqs.message",
		"cronjobsources.sources.eventing.knative.dev":         "dev.knative.cronjob.event",
		"kuberneteseventsources.sources.eventing.knative.dev": "dev.knative.k8s.event",
		// Can be another eventType in GCPPubSub?
		"gcppubsubsources.sources.eventing.knative.dev": "google.pubsub.topic.publish",
		// What about container sources eventTypes?
		"containersources.sources.eventing.knative.dev": "dev.knative.container.event",
	}
)

// Add creates a new Namespace Controller and adds it to the
// Manager with default RBAC. The Manager will set fields on the
// Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {

	r := &reconciler{
		recorder: mgr.GetRecorder(controllerAgentName),
	}

	p := &sdk.Provider{
		AgentName: controllerAgentName,
		Parent:    &corev1.Namespace{},
		Mappers: map[runtime.Object]handler.Mapper{
			&v1beta1.CustomResourceDefinition{}: &mapSourceCrdToEventingNamespaces{r: r}},
		Reconciler: r,
	}

	return p.Add(mgr)
}

type reconciler struct {
	client   client.Client
	scheme   *runtime.Scheme
	recorder record.EventRecorder
}

// mapSourceCrdToEventingNamespaces maps sources CRDs changes to all the eventing Namespaces.
type mapSourceCrdToEventingNamespaces struct {
	r *reconciler
}

func (m *mapSourceCrdToEventingNamespaces) Map(o handler.MapObject) []reconcile.Request {
	ctx := context.Background()
	eventingNamespaces := make([]reconcile.Request, 0)

	crd, ok := o.Object.(*v1beta1.CustomResourceDefinition)
	if !ok {
		return eventingNamespaces
	}

	if crd.Labels[eventingSourceLabelKey] != eventingSourceLabelValue {
		return eventingNamespaces
	}

	opts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(namespaceLabels()),
		// Set Raw because if we need to get more than one page, then we will put the continue token
		// into opts.Raw.Continue.
		Raw: &metav1.ListOptions{},
	}
	for {
		nl := &corev1.NamespaceList{}
		if err := m.r.client.List(ctx, opts, nl); err != nil {
			return eventingNamespaces
		}

		for _, n := range nl.Items {
			eventingNamespaces = append(eventingNamespaces, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      n.Name,
					Namespace: n.Namespace,
				},
			})
			if nl.Continue != "" {
				opts.Raw.Continue = nl.Continue
			} else {
				return eventingNamespaces
			}
		}
	}
}

func (r *reconciler) Reconcile(ctx context.Context, object runtime.Object) error {
	logger := logging.FromContext(ctx)

	namespace, ok := object.(*corev1.Namespace)
	if !ok {
		logger.Errorf("could not find namespace %v", object)
		return nil
	}

	// See if the namespace has been deleted.
	accessor, err := meta.Accessor(namespace)
	if err != nil {
		logger.Warnf("Failed to get metadata accessor: %s", zap.Error(err))
		return err
	}

	if accessor.GetDeletionTimestamp() == nil {
		return r.reconcile(ctx, namespace)
	}
	// No need for a finalizer. If the Namespace is being deleted, then all the objects in it will be too.
	return nil
}

// This reconciliation process just creates EventTypes on eventing enabled namespaces,
// We are not watching on EventTypes changes. If they are deleted, only on Namespaces changes will be re-created.
// Also, if the eventing annotation is disabled, we are not removing the EventTypes. We might need to do that.
func (r *reconciler) reconcile(ctx context.Context, namespace *corev1.Namespace) error {
	logger := logging.FromContext(ctx)

	// TODO try to not be called in this case.
	if namespace.Labels[knativeEventingLabelKey] != knativeEventingLabelValue {
		logger.Debugf("Not reconciling namespace %q", namespace.Name)
		return nil
	}

	logger.Infof("Reconciling Namespace %q", namespace.Name)

	err := r.reconcileNamespace(ctx, namespace)
	if err != nil {
		return err
	}

	logger.Infof("Reconciled Namespace %q", namespace.Name)

	return nil
}

func (r *reconciler) reconcileNamespace(ctx context.Context, namespace *corev1.Namespace) error {
	logger := logging.FromContext(ctx)
	sourcesCrds, err := r.getSourcesCrds(ctx)
	if err != nil {
		return err
	}

	for _, crd := range sourcesCrds {
		err := r.reconcileEventTypes(ctx, crd, namespace)
		if err != nil {
			logger.Errorf("Error reconciling EventTypes from CRD %q for namespace %q", crd.Name, namespace.Name)
			return err
		}
		logger.Infof("Reconciled EventTypes from CRD %q for namespace %q", crd.Name, namespace.Name)
	}
	return nil
}

func (r *reconciler) reconcileEventTypes(ctx context.Context, crd v1beta1.CustomResourceDefinition, namespace *corev1.Namespace) error {
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
				r.recorder.Eventf(namespace, corev1.EventTypeWarning, eventTypesCreateFailed, "Could not create event type %q from CRD %q for namespace %q", eventType.Spec.Type, crd.Name, namespace.Name)
				return err
			}
		}
		r.recorder.Eventf(namespace, corev1.EventTypeNormal, eventTypesCreated, "Event types from CRD %q created for namespace %q, total %d", crd.Name, namespace.Name, len(diff))
	}
	return nil
}

func (r *reconciler) getEventTypes(ctx context.Context, crd v1beta1.CustomResourceDefinition, namespace *corev1.Namespace) ([]eventingv1alpha1.EventType, error) {
	eventTypes := make([]eventingv1alpha1.EventType, 0)

	opts := &client.ListOptions{
		Namespace:     namespace.Name,
		LabelSelector: labels.SelectorFromSet(eventTypeLabels(crd.Name)),
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

func (r *reconciler) newEventTypes(crd v1beta1.CustomResourceDefinition, namespace *corev1.Namespace) []eventingv1alpha1.EventType {
	eventTypes := make([]eventingv1alpha1.EventType, 0)
	if crd.Spec.Validation != nil && crd.Spec.Validation.OpenAPIV3Schema != nil {
		properties := crd.Spec.Validation.OpenAPIV3Schema.Properties
		if spec, ok := properties["spec"]; ok {
			if eTypes, ok := spec.Properties["eventTypes"]; ok {
				if eTypes.Items != nil && eTypes.Items.Schema != nil {
					for _, eType := range eTypes.Items.Schema.Enum {
						// Remove quotes.
						et := strings.Trim(string(eType.Raw), `"`)
						eventType := resources.MakeEventType(crd, namespace, et)
						eventTypes = append(eventTypes, eventType)
					}
				}
			} else {
				// This ugly hack is just for sources that do not specify the eventTypes they can produce.
				// We should probably try to "standarize" CRD spec fields across sources.
				// At least to say what eventTypes they can produce.
				if et, ok := eventTypeFromCrd[crd.Name]; ok {
					eventType := resources.MakeEventType(crd, namespace, et)
					eventTypes = append(eventTypes, eventType)
				}
			}
		}
	}
	return eventTypes
}

func (r *reconciler) getSourcesCrds(ctx context.Context) ([]v1beta1.CustomResourceDefinition, error) {
	sourcesCrds := make([]v1beta1.CustomResourceDefinition, 0)

	opts := &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(sourceLabels()),
		// Set Raw because if we need to get more than one page, then we will put the continue token
		// into opts.Raw.Continue.
		Raw: &metav1.ListOptions{},
	}
	for {
		nl := &v1beta1.CustomResourceDefinitionList{}
		if err := r.client.List(ctx, opts, nl); err != nil {
			return nil, err
		}

		for _, s := range nl.Items {
			sourcesCrds = append(sourcesCrds, s)
		}
		if nl.Continue != "" {
			opts.Raw.Continue = nl.Continue
		} else {
			return sourcesCrds, nil
		}
	}
}

func sourceLabels() map[string]string {
	return map[string]string{
		eventingSourceLabelKey: eventingSourceLabelValue,
	}
}

func namespaceLabels() map[string]string {
	return map[string]string{
		knativeEventingLabelKey: knativeEventingLabelValue,
	}
}

func eventTypeLabels(objName string) map[string]string {
	return map[string]string{
		eventingEventTypeLabelKey: objName,
	}
}

func difference(current []eventingv1alpha1.EventType, expected []eventingv1alpha1.EventType) []eventingv1alpha1.EventType {
	difference := make([]eventingv1alpha1.EventType, 0)
	currentSet := asSet(current)
	for _, e := range expected {
		item := fmt.Sprintf("%s_%s_%s", e.Spec.Type, e.Spec.From, e.Spec.Schema)
		if !currentSet.Has(item) {
			difference = append(difference, e)
		}
	}
	return difference
}

func asSet(eventTypes []eventingv1alpha1.EventType) sets.String {
	set := sets.String{}
	for _, eventType := range eventTypes {
		set.Insert(fmt.Sprintf("%s_%s_%s", eventType.Spec.Type, eventType.Spec.From, eventType.Spec.Schema))
	}
	return set
}

func (r *reconciler) InjectClient(c client.Client) error {
	r.client = c
	return nil
}
