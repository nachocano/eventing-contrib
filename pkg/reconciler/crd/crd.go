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

	logger.Infof("Eventing namespaces %d, %s", len(eventingNamespaces), eventingNamespaces)
	return nil
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

//func (r *reconciler) reconcile(ctx context.Context, ns *corev1.Namespace) error {
//	// No need for a finalizer, because everything reconciled is created inside the Namespace. If
//	// the Namespace is being deleted, then all the reconciled objects will be too.
//
//	if ns.DeletionTimestamp != nil {
//		return nil
//	}
//
//	sa, err := r.reconcileBrokerFilterServiceAccount(ctx, ns)
//	if err != nil {
//		logging.FromContext(ctx).Error("Unable to reconcile the Broker Filter Service Account for the namespace", zap.Error(err))
//		return err
//	}
//	_, err = r.reconcileBrokerFilterRBAC(ctx, ns, sa)
//	if err != nil {
//		logging.FromContext(ctx).Error("Unable to reconcile the Broker Filter Service Account RBAC for the namespace", zap.Error(err))
//		return err
//	}
//
//	sa, err = r.reconcileBrokerIngressServiceAccount(ctx, ns)
//	if err != nil {
//		logging.FromContext(ctx).Error("Unable to reconcile the Broker Ingress Service Account for the namespace", zap.Error(err))
//		return err
//	}
//	_, err = r.reconcileBrokerIngressRBAC(ctx, ns, sa)
//	if err != nil {
//		logging.FromContext(ctx).Error("Unable to reconcile the Broker Ingress Service Account RBAC for the namespace", zap.Error(err))
//		return err
//	}
//	_, err = r.reconcileBroker(ctx, ns)
//	if err != nil {
//		logging.FromContext(ctx).Error("Unable to reconcile Broker for the namespace", zap.Error(err))
//		return err
//	}
//
//	return nil
//}
//
//// reconcileBrokerFilterServiceAccount reconciles the Broker's filter service account for Namespace 'ns'.
//func (r *reconciler) reconcileBrokerFilterServiceAccount(ctx context.Context, ns *corev1.Namespace) (*corev1.ServiceAccount, error) {
//	return r.reconcileBrokerServiceAccount(ctx, ns, brokerFilterSA, filterServiceAccountCreated)
//}
//
//// reconcileBrokerIngressServiceAccount reconciles the Broker's ingress service account for Namespace 'ns'.
//func (r *reconciler) reconcileBrokerIngressServiceAccount(ctx context.Context, ns *corev1.Namespace) (*corev1.ServiceAccount, error) {
//	// TODO in order to get cluster-scoped EventTypes, we need a ClusterRoleBinding.
//	return r.reconcileBrokerServiceAccount(ctx, ns, brokerIngressSA, ingressServiceAccountCreated)
//}
//
//// reconcileBrokerServiceAccount reconciles the Broker's service account called 'serviceAccount' for Namespace 'ns'.
//func (r *reconciler) reconcileBrokerServiceAccount(ctx context.Context, ns *corev1.Namespace, serviceAccount string, eventName string) (*corev1.ServiceAccount, error) {
//	current, err := r.getBrokerServiceAccount(ctx, ns, serviceAccount)
//
//	// If the resource doesn't exist, we'll create it.
//	if k8serrors.IsNotFound(err) {
//		sa := newBrokerServiceAccount(ns, serviceAccount)
//		err = r.client.Create(ctx, sa)
//		if err != nil {
//			return nil, err
//		}
//		r.recorder.Event(ns,
//			corev1.EventTypeNormal,
//			eventName,
//			fmt.Sprintf("Service account created for the Broker %q", sa.Name))
//		return sa, nil
//	} else if err != nil {
//		return nil, err
//	}
//	// Don't update anything that is already present.
//	return current, nil
//}
//
//// getBrokerServiceAccount returns the Broker's service account for Namespace 'ns' called 'serviceAccount' if it exists,
//// otherwise it returns an error.
//func (r *reconciler) getBrokerServiceAccount(ctx context.Context, ns *corev1.Namespace, serviceAccount string) (*corev1.ServiceAccount, error) {
//	sa := &corev1.ServiceAccount{}
//	name := types.NamespacedName{
//		Namespace: ns.Name,
//		Name:      serviceAccount,
//	}
//	err := r.client.Get(ctx, name, sa)
//	return sa, err
//}
//
//// newBrokerServiceAccount creates a ServiceAccount object for the Namespace 'ns' called 'serviceAccount'.
//func newBrokerServiceAccount(ns *corev1.Namespace, serviceAccount string) *corev1.ServiceAccount {
//	return &corev1.ServiceAccount{
//		ObjectMeta: metav1.ObjectMeta{
//			Namespace: ns.Name,
//			Name:      serviceAccount,
//			Labels:    injectedLabels(),
//		},
//	}
//}
//
//func injectedLabels() map[string]string {
//	return map[string]string{
//		"eventing.knative.dev/namespaceInjected": "true",
//	}
//}
//
//// reconcileBrokerFilterRBAC reconciles the Broker's filter service account RBAC for the Namespace 'ns'.
//func (r *reconciler) reconcileBrokerFilterRBAC(ctx context.Context, ns *corev1.Namespace, sa *corev1.ServiceAccount) (*rbacv1.RoleBinding, error) {
//	return r.reconcileBrokerRBAC(ctx, ns, sa, brokerFilterRB, brokerFilterClusterRole, filterServiceAccountRBACCreated)
//}
//
//// reconcileBrokerIngressRBAC reconciles the Broker's ingress service account RBAC for the Namespace 'ns'.
//func (r *reconciler) reconcileBrokerIngressRBAC(ctx context.Context, ns *corev1.Namespace, sa *corev1.ServiceAccount) (*rbacv1.RoleBinding, error) {
//	return r.reconcileBrokerRBAC(ctx, ns, sa, brokerIngressRB, brokerIngressClusterRole, ingressServiceAccountRBACCreated)
//}
//
//// reconcileBrokerRBAC reconciles the Broker's service account RBAC for the RoleBinding 'roleBinding', ClusterRole
//// 'clusterRole', in Namespace 'ns'.
//func (r *reconciler) reconcileBrokerRBAC(ctx context.Context, ns *corev1.Namespace, sa *corev1.ServiceAccount, roleBinding, clusterRole, eventName string) (*rbacv1.RoleBinding, error) {
//	current, err := r.getBrokerRBAC(ctx, ns, roleBinding)
//
//	// If the resource doesn't exist, we'll create it.
//	if k8serrors.IsNotFound(err) {
//		rbac := newBrokerRBAC(ns, sa, roleBinding, clusterRole)
//		err = r.client.Create(ctx, rbac)
//		if err != nil {
//			return nil, err
//		}
//		r.recorder.Event(ns,
//			corev1.EventTypeNormal,
//			eventName,
//			fmt.Sprintf("Service account RBAC created for the Broker %q", rbac.Name))
//		return rbac, nil
//	} else if err != nil {
//		return nil, err
//	}
//	// Don't update anything that is already present.
//	return current, nil
//}
//
//// getBrokerRBAC returns the Broker's role binding for Namespace 'ns' called 'roleBinding' if it exists, otherwise it
//// returns an error.
//func (r *reconciler) getBrokerRBAC(ctx context.Context, ns *corev1.Namespace, roleBinding string) (*rbacv1.RoleBinding, error) {
//	rb := &rbacv1.RoleBinding{}
//	name := types.NamespacedName{
//		Namespace: ns.Name,
//		Name:      roleBinding,
//	}
//	err := r.client.Get(ctx, name, rb)
//	return rb, err
//}
//
//// newBrokerRBAC creates a RpleBinding object for the Broker's service account 'sa' in the Namespace 'ns' called
//// 'roleBinding', with ClusterRole 'clusterRole'.
//func newBrokerRBAC(ns *corev1.Namespace, sa *corev1.ServiceAccount, roleBinding string, clusterRole string) *rbacv1.RoleBinding {
//	return &rbacv1.RoleBinding{
//		ObjectMeta: metav1.ObjectMeta{
//			Namespace: ns.Name,
//			Name:      roleBinding,
//			Labels:    injectedLabels(),
//		},
//		RoleRef: rbacv1.RoleRef{
//			APIGroup: "rbac.authorization.k8s.io",
//			Kind:     "ClusterRole",
//			Name:     clusterRole,
//		},
//		Subjects: []rbacv1.Subject{
//			{
//				Kind:      "ServiceAccount",
//				Namespace: ns.Name,
//				Name:      sa.Name,
//			},
//		},
//	}
//}
//
//// getBroker returns the default broker for Namespace 'ns' if it exists, otherwise it returns an error.
//func (r *reconciler) getBroker(ctx context.Context, ns *corev1.Namespace) (*v1alpha1.Broker, error) {
//	b := &v1alpha1.Broker{}
//	name := types.NamespacedName{
//		Namespace: ns.Name,
//		Name:      defaultBroker,
//	}
//	err := r.client.Get(ctx, name, b)
//	return b, err
//}
//
//// reconcileBroker reconciles the default Broker for the Namespace 'ns'.
//func (r *reconciler) reconcileBroker(ctx context.Context, ns *corev1.Namespace) (*v1alpha1.Broker, error) {
//	current, err := r.getBroker(ctx, ns)
//
//	// If the resource doesn't exist, we'll create it.
//	if k8serrors.IsNotFound(err) {
//		b := newBroker(ns)
//		err = r.client.Create(ctx, b)
//		if err != nil {
//			return nil, err
//		}
//		r.recorder.Event(ns, corev1.EventTypeNormal, brokerCreated, "Default eventing.knative.dev Broker created.")
//		return b, nil
//	} else if err != nil {
//		return nil, err
//	}
//	// Don't update anything that is already present.
//	return current, nil
//}
//
//// newBroker creates a placeholder default Broker object for Namespace 'ns'.
//func newBroker(ns *corev1.Namespace) *v1alpha1.Broker {
//	return &v1alpha1.Broker{
//		ObjectMeta: metav1.ObjectMeta{
//			Namespace: ns.Name,
//			Name:      defaultBroker,
//			Labels:    injectedLabels(),
//		},
//	}
//}
