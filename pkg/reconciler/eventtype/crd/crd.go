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
	"github.com/knative/eventing-sources/pkg/controller/sdk"
	"github.com/knative/eventing-sources/pkg/reconciler/eventtype"
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
)

// Add creates a new CRD Controller and adds it to the
// Manager with default RBAC. The Manager will set fields on the
// Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {

	p := &sdk.Provider{
		AgentName: controllerAgentName,
		Parent:    &v1beta1.CustomResourceDefinition{},
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

// This reconciliation process creates EventTypes when a new source CRD is installed.
// It does so on eventing enabled namespaces.
// As with the namespace controller, we are not watching EventTypes changes. If they are deleted, only on a CRD change,
// they will get re-created.
// Also, if the CRD is removed, we automatically delete the EventTypes it created in the corresponding namespaces.
func (r *reconciler) reconcile(ctx context.Context, crd *v1beta1.CustomResourceDefinition) error {
	logger := logging.FromContext(ctx)

	// TODO try to not be called in this case.
	if crd.Labels[eventtype.EventingSourceLabelKey] != eventtype.EventingSourceLabelValue {
		logger.Debugf("Not reconciling CRD %q", crd.Name)
		return nil
	}

	logger.Infof("Reconciling CRD %q", crd.Name)

	err := r.reconcileCrd(ctx, crd)
	if err != nil {
		return err
	}

	logger.Infof("Reconciled CRD %q", crd.Name)

	return nil
}

func (r *reconciler) reconcileCrd(ctx context.Context, crd *v1beta1.CustomResourceDefinition) error {
	logger := logging.FromContext(ctx)
	eventingNamespaces, err := r.getEventingNamespaces(ctx, crd)
	if err != nil {
		return err
	}

	for _, eventingNamespace := range eventingNamespaces {
		err := r.reconcileEventTypes(ctx, crd, eventingNamespace)
		if err != nil {
			logger.Errorf("Error reconciling EventTypes from CRD %q for namespace %q", crd.Name, eventingNamespace.Name)
			return err
		}
		logger.Infof("Reconciled EventTypes from CRD %q for namespace %q", crd.Name, eventingNamespace.Name)
	}
	return nil
}

func (r *reconciler) reconcileEventTypes(ctx context.Context, crd *v1beta1.CustomResourceDefinition, namespace corev1.Namespace) error {
	return eventtype.ReconcileEventTypes(r.client, r.recorder, ctx, crd, namespace)
}

func (r *reconciler) getEventingNamespaces(ctx context.Context, crd *v1beta1.CustomResourceDefinition) ([]corev1.Namespace, error) {
	eventingNamespaces := make([]corev1.Namespace, 0)

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
			eventingNamespaces = append(eventingNamespaces, e)
		}
		if nl.Continue != "" {
			opts.Raw.Continue = nl.Continue
		} else {
			return eventingNamespaces, nil
		}
	}
}

func (r *reconciler) finalize(ctx context.Context, crd *v1beta1.CustomResourceDefinition) error {
	logger := logging.FromContext(ctx)
	eventingNamespaces, err := r.getEventingNamespaces(ctx, crd)
	if err != nil {
		logger.Errorf("Error finalizing CRD %s", crd.Name)
		return err
	}

	for _, eventingNamespace := range eventingNamespaces {
		err := r.deleteEventTypes(ctx, crd, eventingNamespace)
		if err != nil {
			logger.Errorf("Error deleting EventTypes from CRD %q for namespace %q", crd.Name, eventingNamespace.Name)
			return err
		}
		logger.Infof("Deleted EventTypes from CRD %q for namespace %q", crd.Name, eventingNamespace.Name)
	}
	return nil
}

func (r *reconciler) deleteEventTypes(ctx context.Context, crd *v1beta1.CustomResourceDefinition, namespace corev1.Namespace) error {
	current, err := eventtype.GetEventTypes(r.client, ctx, crd, namespace)
	if err != nil {
		return err
	}
	if len(current) > 0 {
		// TODO bulk deletion?
		for _, eventType := range current {
			err = r.client.Delete(ctx, &eventType)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func namespaceLabels() map[string]string {
	return map[string]string{
		eventtype.KnativeEventingLabelKey: eventtype.KnativeEventingLabelValue,
	}
}

func (r *reconciler) InjectClient(c client.Client) error {
	r.client = c
	return nil
}
