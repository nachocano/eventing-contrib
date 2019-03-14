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
	"github.com/knative/eventing-sources/pkg/controller/sdk"
	"github.com/knative/eventing-sources/pkg/reconciler/eventtype"
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
)

// Add creates a new CRD Controller and adds it to the
// Manager with default RBAC. The Manager will set fields on the
// Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {

	p := &sdk.Provider{
		AgentName: controllerAgentName,
		Parent:    &corev1.Namespace{},

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

	// TODO try to not be even called in this case.
	if namespace.Labels[eventtype.KnativeEventingLabelKey] != eventtype.KnativeEventingLabelValue {
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
	return eventtype.ReconcileEventTypes(r.client, r.recorder, ctx, &crd, *namespace)
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
		eventtype.EventingSourceLabelKey: eventtype.EventingSourceLabelValue,
	}
}

func (r *reconciler) InjectClient(c client.Client) error {
	r.client = c
	return nil
}
