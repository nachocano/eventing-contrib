/*
Copyright 2018 The Knative Authors

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

package githubsource

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"sigs.k8s.io/controller-runtime/pkg/handler"

	sourcesv1alpha1 "github.com/knative/eventing-sources/pkg/apis/sources/v1alpha1"
	"github.com/knative/eventing-sources/pkg/controller/sdk"
	"github.com/knative/eventing-sources/pkg/controller/sinks"
	"github.com/knative/eventing-sources/pkg/reconciler/githubsource/resources"
	eventtype "github.com/knative/eventing-sources/pkg/resources"
	eventingv1alpha1 "github.com/knative/eventing/pkg/apis/eventing/v1alpha1"
	"github.com/knative/pkg/logging"
	servingv1alpha1 "github.com/knative/serving/pkg/apis/serving/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// controllerAgentName is the string used by this controller to identify
	// itself when creating events.
	controllerAgentName = "github-source-controller"
	raImageEnvVar       = "GH_RA_IMAGE"
	finalizerName       = controllerAgentName

	eventTypeControllerLabelKey = "eventing.knative.dev/eventtype-source"
	eventTypeSourceLabelKey     = "eventing.knative.dev/eventtype-source-name"
)

// Add creates a new GitHubSource Controller and adds it to the
// Manager with default RBAC. The Manager will set fields on the
// Controller and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	receiveAdapterImage, defined := os.LookupEnv(raImageEnvVar)
	if !defined {
		return fmt.Errorf("required environment variable %q not defined", raImageEnvVar)
	}

	r := &reconciler{
		recorder:            mgr.GetRecorder(controllerAgentName),
		scheme:              mgr.GetScheme(),
		receiveAdapterImage: receiveAdapterImage,
		webhookClient:       gitHubWebhookClient{},
	}

	log.Println("Adding the GitHub Source controller.")
	p := &sdk.Provider{
		AgentName: controllerAgentName,
		Parent:    &sourcesv1alpha1.GitHubSource{},
		Owns:      []runtime.Object{&servingv1alpha1.Service{}},
		Mappers: map[runtime.Object]handler.Mapper{
			&eventingv1alpha1.EventType{}: &mapEventTypeToGitHubSource{r: r}},
		Reconciler: r,
	}

	return p.Add(mgr)
}

// reconciler reconciles a GitHubSource object
type reconciler struct {
	client              client.Client
	scheme              *runtime.Scheme
	recorder            record.EventRecorder
	receiveAdapterImage string
	webhookClient       webhookClient
}

// mapEventTypeToGitHubSource maps EventTypes changes to the GitHub source that created it.
type mapEventTypeToGitHubSource struct {
	r *reconciler
}

func (b *mapEventTypeToGitHubSource) Map(o handler.MapObject) []reconcile.Request {
	ctx := context.Background()
	gitHubSources := make([]reconcile.Request, 0)

	opts := &client.ListOptions{
		Namespace: o.Meta.GetNamespace(),
		// Set Raw because if we need to get more than one page, then we will put the continue token
		// into opts.Raw.Continue.
		Raw: &metav1.ListOptions{},
	}
	for {
		etl := &sourcesv1alpha1.GitHubSourceList{}
		if err := b.r.client.List(ctx, opts, etl); err != nil {
			return gitHubSources
		}

		for _, et := range etl.Items {
			if label, ok := et.Labels[eventTypeControllerLabelKey]; ok {
				if label == controllerAgentName && et.Spec.Sink != nil && et.Spec.Sink.Kind == "Broker" {
					gitHubSources = append(gitHubSources, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Namespace: et.Namespace,
							Name:      et.Name,
						},
					})

				}
			}
		}
		if etl.Continue != "" {
			opts.Raw.Continue = etl.Continue
		} else {
			return gitHubSources
		}
	}
}

// Reconcile reads that state of the cluster for a GitHubSource
// object and makes changes based on the state read and what is in the
// GitHubSource.Spec
func (r *reconciler) Reconcile(ctx context.Context, object runtime.Object) error {
	logger := logging.FromContext(ctx)

	source, ok := object.(*sourcesv1alpha1.GitHubSource)
	if !ok {
		logger.Errorf("could not find github source %v\n", object)
		return nil
	}

	// See if the source has been deleted
	accessor, err := meta.Accessor(source)
	if err != nil {
		logger.Warnf("Failed to get metadata accessor: %s", zap.Error(err))
		return err
	}

	var reconcileErr error
	if accessor.GetDeletionTimestamp() == nil {
		reconcileErr = r.reconcile(ctx, source)
	} else {
		reconcileErr = r.finalize(ctx, source)
	}

	return reconcileErr
}

func (r *reconciler) reconcile(ctx context.Context, source *sourcesv1alpha1.GitHubSource) error {
	source.Status.InitializeConditions()

	accessToken, err := r.secretFrom(ctx, source.Namespace, source.Spec.AccessToken.SecretKeyRef)
	if err != nil {
		source.Status.MarkNoSecrets("AccessTokenNotFound", "%s", err)
		return err
	}
	secretToken, err := r.secretFrom(ctx, source.Namespace, source.Spec.SecretToken.SecretKeyRef)
	if err != nil {
		source.Status.MarkNoSecrets("SecretTokenNotFound", "%s", err)
		return err
	}
	source.Status.MarkSecrets()

	uri, err := sinks.GetSinkURI(ctx, r.client, source.Spec.Sink, source.Namespace)
	if err != nil {
		source.Status.MarkNoSink("NotFound", "%s", err)
		return err
	}
	source.Status.MarkSink(uri)

	ksvc, err := r.getOwnedService(ctx, source)
	if err != nil {
		if apierrors.IsNotFound(err) {
			ksvc = resources.MakeService(source, r.receiveAdapterImage)
			if err = controllerutil.SetControllerReference(source, ksvc, r.scheme); err != nil {
				return err
			}
			if err = r.client.Create(ctx, ksvc); err != nil {
				return err
			}
			r.recorder.Eventf(source, corev1.EventTypeNormal, "ServiceCreated", "Created Service %q", ksvc.Name)
			// TODO: Mark Deploying for the ksvc
			// Wait for the Service to get a status
			return nil
		}
		// Error was something other than NotFound
		return err
	}

	routeCondition := ksvc.Status.GetCondition(servingv1alpha1.ServiceConditionRoutesReady)
	receiveAdapterDomain := ksvc.Status.Domain
	if routeCondition != nil && routeCondition.Status == corev1.ConditionTrue && receiveAdapterDomain != "" {
		// TODO: Mark Deployed for the ksvc
		// TODO: Mark some condition for the webhook status?
		r.addFinalizer(source)
		if source.Status.WebhookIDKey == "" {
			hookID, err := r.createWebhook(ctx, source,
				receiveAdapterDomain, accessToken, secretToken, source.Spec.GitHubAPIURL)
			if err != nil {
				return err
			}
			source.Status.WebhookIDKey = hookID

			// Only create EventTypes for Broker sinks.
			if source.Spec.Sink.Kind == "Broker" {
				err = r.reconcileEventTypes(ctx, source)
				if err != nil {
					return err
				}
			}
			// We mark the event types in order to have the source Ready.
			source.Status.MarkEventTypes()
		}
	}

	return nil
}

func (r *reconciler) finalize(ctx context.Context, source *sourcesv1alpha1.GitHubSource) error {
	// Always remove the finalizer. If there's a failure cleaning up, an event
	// will be recorded allowing the webhook to be removed manually by the
	// operator.
	r.removeFinalizer(source)

	// If a webhook was created, try to delete it
	if source.Status.WebhookIDKey != "" {
		// Get access token
		accessToken, err := r.secretFrom(ctx, source.Namespace, source.Spec.AccessToken.SecretKeyRef)
		if err != nil {
			source.Status.MarkNoSecrets("AccessTokenNotFound", "%s", err)
			r.recorder.Eventf(source, corev1.EventTypeWarning, "FailedFinalize", "Could not delete webhook %q: %v", source.Status.WebhookIDKey, err)
			return err
		}

		// Delete the webhook using the access token and stored webhook ID
		err = r.deleteWebhook(ctx, source, accessToken, source.Status.WebhookIDKey, source.Spec.GitHubAPIURL)
		if err != nil {
			r.recorder.Eventf(source, corev1.EventTypeWarning, "FailedFinalize", "Could not delete webhook %q: %v", source.Status.WebhookIDKey, err)
			return err
		}
		// Webhook deleted, clear ID
		source.Status.WebhookIDKey = ""
	}

	return nil
}

func (r *reconciler) createWebhook(ctx context.Context, source *sourcesv1alpha1.GitHubSource, domain, accessToken, secretToken, alternateGitHubAPIURL string) (string, error) {
	// TODO: Modify function args to shorten method signature ... something like
	// func (r *reconciler) createWebhook(ctx context.Context, args webhookArgs) (string, error) {...}
	// where webhookArgs is a struct like....
	// type webhookArgs struct {
	//  source *sourcesv1alpha1.GitHubSource
	//  domain string
	//  accessToken string
	//  secretToken string
	//  alternateGitHubAPIURL string
	//  hookID string
	// }
	logger := logging.FromContext(ctx)

	logger.Info("creating GitHub webhook")

	owner, repo, err := parseOwnerRepoFrom(source.Spec.OwnerAndRepository)
	if err != nil {
		return "", err
	}

	hookOptions := &webhookOptions{
		accessToken: accessToken,
		secretToken: secretToken,
		domain:      domain,
		owner:       owner,
		repo:        repo,
		events:      source.Spec.EventTypes,
		secure:      source.Spec.Secure,
	}
	hookID, err := r.webhookClient.Create(ctx, hookOptions, alternateGitHubAPIURL)
	if err != nil {
		return "", fmt.Errorf("failed to create webhook: %v", err)
	}
	return hookID, nil
}

func (r *reconciler) deleteWebhook(ctx context.Context, source *sourcesv1alpha1.GitHubSource, accessToken, hookID, alternateGitHubAPIURL string) error {
	// TODO: Modify function args to shorten method signature ... something like
	// func (r *reconciler) deleteWebhook(ctx context.Context, args webhookArgs) (string, error) {...}
	// where webhookArgs is a struct like....
	// type webhookArgs struct {
	//  source *sourcesv1alpha1.GitHubSource
	//  domain string
	//  accessToken string
	//  secretToken string
	//  alternateGitHubAPIURL string
	//  hookID string
	// }
	logger := logging.FromContext(ctx)

	logger.Info("deleting GitHub webhook")

	owner, repo, err := parseOwnerRepoFrom(source.Spec.OwnerAndRepository)
	if err != nil {
		return err
	}

	hookOptions := &webhookOptions{
		accessToken: accessToken,
		owner:       owner,
		repo:        repo,
		events:      source.Spec.EventTypes,
		secure:      source.Spec.Secure,
	}
	err = r.webhookClient.Delete(ctx, hookOptions, hookID, alternateGitHubAPIURL)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %v", err)
	}
	return nil
}

func (r *reconciler) secretFrom(ctx context.Context, namespace string, secretKeySelector *corev1.SecretKeySelector) (string, error) {
	secret := &corev1.Secret{}
	err := r.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretKeySelector.Name}, secret)
	if err != nil {
		return "", err
	}
	secretVal, ok := secret.Data[secretKeySelector.Key]
	if !ok {
		return "", fmt.Errorf(`key "%s" not found in secret "%s"`, secretKeySelector.Key, secretKeySelector.Name)
	}
	return string(secretVal), nil
}

func parseOwnerRepoFrom(ownerAndRepository string) (string, string, error) {
	components := strings.Split(ownerAndRepository, "/")
	if len(components) > 2 {
		return "", "", fmt.Errorf("ownerAndRepository is malformatted, expected 'owner/repository' but found %q", ownerAndRepository)
	}
	owner := components[0]
	if len(owner) == 0 && len(components) > 1 {
		return "", "", fmt.Errorf("owner is empty, expected 'owner/repository' but found %q", ownerAndRepository)
	}
	repo := ""
	if len(components) > 1 {
		repo = components[1]
	}

	return owner, repo, nil
}

func (r *reconciler) getOwnedService(ctx context.Context, source *sourcesv1alpha1.GitHubSource) (*servingv1alpha1.Service, error) {
	list := &servingv1alpha1.ServiceList{}
	err := r.client.List(ctx, &client.ListOptions{
		Namespace:     source.Namespace,
		LabelSelector: labels.Everything(),
		// TODO this is here because the fake client needs it.
		// Remove this when it's no longer needed.
		Raw: &metav1.ListOptions{
			TypeMeta: metav1.TypeMeta{
				APIVersion: servingv1alpha1.SchemeGroupVersion.String(),
				Kind:       "Service",
			},
		},
	},
		list)
	if err != nil {
		return nil, err
	}
	for _, ksvc := range list.Items {
		if metav1.IsControlledBy(&ksvc, source) {
			//TODO if there are >1 controlled, delete all but first?
			return &ksvc, nil
		}
	}
	return nil, apierrors.NewNotFound(servingv1alpha1.Resource("services"), "")
}

func (r *reconciler) reconcileEventTypes(ctx context.Context, source *sourcesv1alpha1.GitHubSource) error {
	current, err := r.getEventTypes(ctx, source)
	if err != nil {
		return err
	}
	expected, err := r.newEventTypes(source)
	if err != nil {
		return err
	}

	diff := eventtype.Difference(current, expected)
	if len(diff) > 0 {
		// As EventTypes are immutable, if we have any diff it means that something was deleted or not even created
		// in the first place. We should create them.
		for _, eventType := range diff {
			err = r.client.Create(ctx, eventType)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *reconciler) getEventTypes(ctx context.Context, source *sourcesv1alpha1.GitHubSource) ([]*eventingv1alpha1.EventType, error) {
	eventTypes := make([]*eventingv1alpha1.EventType, 0)

	opts := &client.ListOptions{
		Namespace:     source.Namespace,
		LabelSelector: labels.SelectorFromSet(getEventTypeSourceLabels(source)),
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
			if metav1.IsControlledBy(&e, source) {
				eventTypes = append(eventTypes, &e)
			}
		}
		if el.Continue != "" {
			opts.Raw.Continue = el.Continue
		} else {
			return eventTypes, nil
		}
	}
}

func (r *reconciler) newEventTypes(source *sourcesv1alpha1.GitHubSource) ([]*eventingv1alpha1.EventType, error) {
	eventTypes := make([]*eventingv1alpha1.EventType, 0)
	for _, et := range source.Spec.EventTypes {
		args := &eventtype.EventTypeArgs{
			Type:      fmt.Sprintf("%s.%s", sourcesv1alpha1.GitHubSourceEventPrefix, et),
			Source:    source.Spec.OwnerAndRepository,
			Broker:    source.Spec.Sink.Name,
			Namespace: source.Namespace,
			Labels:    getEventTypeSourceLabels(source),
			// TODO change CRD to set the schema.
			Schema: "",
		}
		eventType := eventtype.MakeEventType(args)
		// Setting the reference to delete the EventType upon uninstallation of the source.
		if err := controllerutil.SetControllerReference(source, eventType, r.scheme); err != nil {
			return nil, err
		}
		eventTypes = append(eventTypes, eventType)
	}
	return eventTypes, nil
}

func getEventTypeSourceLabels(src *sourcesv1alpha1.GitHubSource) map[string]string {
	return map[string]string{
		eventTypeControllerLabelKey: controllerAgentName,
		eventTypeSourceLabelKey:     src.Name,
	}
}

func (r *reconciler) addFinalizer(s *sourcesv1alpha1.GitHubSource) {
	finalizers := sets.NewString(s.Finalizers...)
	finalizers.Insert(finalizerName)
	s.Finalizers = finalizers.List()
}

func (r *reconciler) removeFinalizer(s *sourcesv1alpha1.GitHubSource) {
	finalizers := sets.NewString(s.Finalizers...)
	finalizers.Delete(finalizerName)
	s.Finalizers = finalizers.List()
}

func (r *reconciler) InjectClient(c client.Client) error {
	r.client = c
	return nil
}
