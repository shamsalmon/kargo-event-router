// Package dispatch watches Kubernetes Events recorded by Kargo and delivers
// them to the destinations described by EventRoute resources in the same
// namespace (i.e. the same Kargo Project).
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlevent "sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"

	"github.com/shamsalmon/kargo-event-router/api/v1alpha1"
	"github.com/shamsalmon/kargo-event-router/pkg/payload"
	"github.com/shamsalmon/kargo-event-router/pkg/sink"
)

// annotationKeyRoutedTo is set on Kubernetes Events to record the names of
// the EventRoutes to which the event has already been delivered. This is what
// makes delivery idempotent across retries and controller restarts.
const annotationKeyRoutedTo = "kargo-event-router.io/routed-to"

// signingKeySecretKey is the key in a referenced Secret's data map holding
// the HMAC signing key.
const signingKeySecretKey = "secret"

// ReconcilerConfig represents configuration for the event dispatch
// reconciler.
type ReconcilerConfig struct {
	// MaxEventAge is the maximum age an event may have to still be eligible
	// for delivery. This prevents replaying old events when the controller
	// starts (or restarts) and resyncs.
	MaxEventAge time.Duration `envconfig:"MAX_EVENT_AGE" default:"30m"`
	// SendTimeout is the per-request timeout for deliveries to sinks.
	SendTimeout time.Duration `envconfig:"SEND_TIMEOUT" default:"10s"`
}

// ReconcilerConfigFromEnv returns a ReconcilerConfig populated from
// environment variables.
func ReconcilerConfigFromEnv() ReconcilerConfig {
	cfg := ReconcilerConfig{}
	envconfig.MustProcess("", &cfg)
	return cfg
}

type reconciler struct {
	client    client.Client
	apiReader client.Reader
	cfg       ReconcilerConfig
	// newSinkFn constructs the Sink for a delivery. It is a field so tests
	// can substitute a fake.
	newSinkFn func(url string, signingKey []byte, timeout time.Duration) sink.Sink
	// nowFn returns the current time. It is a field so tests can control it.
	nowFn func() time.Time
}

func newReconciler(
	kubeClient client.Client,
	apiReader client.Reader,
	cfg ReconcilerConfig,
) *reconciler {
	return &reconciler{
		client:    kubeClient,
		apiReader: apiReader,
		cfg:       cfg,
		newSinkFn: sink.NewWebhookSink,
		nowFn:     time.Now,
	}
}

// SetupReconcilerWithManager initializes the event dispatch reconciler and
// registers it with the provided Manager.
//
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups=kargo-event-router.io,resources=eventroutes,verbs=get;list;watch
func SetupReconcilerWithManager(
	mgr manager.Manager,
	cfg ReconcilerConfig,
) error {
	r := newReconciler(mgr.GetClient(), mgr.GetAPIReader(), cfg)
	return ctrl.NewControllerManagedBy(mgr).
		For(
			&corev1.Event{},
			builder.WithPredicates(predicate.Funcs{
				// Kargo's event recorder only ever creates Events -- it never
				// aggregates or updates them -- so creation is the only
				// interesting watch event. Updates are filtered out so this
				// reconciler's own annotation patches do not retrigger it.
				CreateFunc: func(e ctrlevent.CreateEvent) bool {
					return r.isRoutable(e.Object)
				},
				UpdateFunc:  func(ctrlevent.UpdateEvent) bool { return false },
				DeleteFunc:  func(ctrlevent.DeleteEvent) bool { return false },
				GenericFunc: func(ctrlevent.GenericEvent) bool { return false },
			}),
		).
		Named("event_dispatcher").
		Complete(r)
}

func (r *reconciler) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	evt := &corev1.Event{}
	if err := r.client.Get(ctx, req.NamespacedName, evt); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !r.isRoutable(evt) {
		return ctrl.Result{}, nil
	}

	routes := &v1alpha1.EventRouteList{}
	if err := r.client.List(
		ctx,
		routes,
		client.InNamespace(evt.Namespace),
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("error listing EventRoutes: %w", err)
	}

	delivered := routedTo(evt)
	var pending []*v1alpha1.EventRoute
	for i := range routes.Items {
		route := &routes.Items[i]
		if slices.Contains(delivered, route.Name) {
			continue
		}
		if routeMatches(route, evt) {
			pending = append(pending, route)
		}
	}
	if len(pending) == 0 {
		return ctrl.Result{}, nil
	}

	ce, err := payload.New(evt)
	if err != nil {
		// The event's annotations are malformed. Retrying cannot fix this,
		// so log and drop the event rather than retrying forever.
		logger.Error(err, "error building payload; dropping event")
		return ctrl.Result{}, nil
	}
	body, err := json.Marshal(ce)
	if err != nil {
		logger.Error(err, "error marshaling payload; dropping event")
		return ctrl.Result{}, nil
	}

	// Deliver to each pending route, marking the event after each successful
	// delivery so a retry triggered by one route's failure does not
	// re-deliver to routes that already succeeded.
	var errs []error
	for _, route := range pending {
		if err = r.deliver(ctx, route, body); err != nil {
			errs = append(
				errs,
				fmt.Errorf("error delivering to EventRoute %q: %w", route.Name, err),
			)
			continue
		}
		logger.Info(
			"delivered event",
			"reason", evt.Reason,
			"route", route.Name,
		)
		if err = r.markRouted(ctx, evt, route.Name); err != nil {
			errs = append(errs, fmt.Errorf(
				"error marking event as routed to EventRoute %q: %w",
				route.Name, err,
			))
		}
	}
	return ctrl.Result{}, errors.Join(errs...)
}

// isRoutable returns true if the given object is a Kubernetes Event recorded
// by Kargo that is recent enough to still be worth delivering.
func (r *reconciler) isRoutable(obj client.Object) bool {
	evt, ok := obj.(*corev1.Event)
	if !ok {
		return false
	}
	if evt.InvolvedObject.APIVersion != kargoapi.GroupVersion.String() {
		return false
	}
	return r.nowFn().Sub(eventTime(evt)) <= r.cfg.MaxEventAge
}

// deliver sends the given payload to the given EventRoute's destination.
func (r *reconciler) deliver(
	ctx context.Context,
	route *v1alpha1.EventRoute,
	body []byte,
) error {
	var signingKey []byte
	if ref := route.Spec.Webhook.SecretRef; ref != nil {
		// Secrets in project namespaces are deliberately read with the
		// uncached reader so the controller does not have to watch and cache
		// Secrets cluster-wide.
		secret := &corev1.Secret{}
		if err := r.apiReader.Get(
			ctx,
			client.ObjectKey{Namespace: route.Namespace, Name: ref.Name},
			secret,
		); err != nil {
			return fmt.Errorf("error getting Secret %q: %w", ref.Name, err)
		}
		key, ok := secret.Data[signingKeySecretKey]
		if !ok {
			return fmt.Errorf(
				"Secret %q has no %q key", ref.Name, signingKeySecretKey,
			)
		}
		signingKey = key
	}
	return r.newSinkFn(route.Spec.Webhook.URL, signingKey, r.cfg.SendTimeout).
		Send(ctx, body)
}

// markRouted records on the event that it has been delivered to the named
// EventRoute.
func (r *reconciler) markRouted(
	ctx context.Context,
	evt *corev1.Event,
	routeName string,
) error {
	patch := client.MergeFrom(evt.DeepCopy())
	if evt.Annotations == nil {
		evt.Annotations = map[string]string{}
	}
	evt.Annotations[annotationKeyRoutedTo] = strings.Join(
		append(routedTo(evt), routeName),
		",",
	)
	return r.client.Patch(ctx, evt, patch)
}

// routedTo returns the names of the EventRoutes to which the given event has
// already been delivered.
func routedTo(evt *corev1.Event) []string {
	var names []string
	for _, name := range strings.Split(
		evt.Annotations[annotationKeyRoutedTo], ",",
	) {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// routeMatches returns true if the given EventRoute applies to the given
// event.
func routeMatches(route *v1alpha1.EventRoute, evt *corev1.Event) bool {
	if len(route.Spec.EventTypes) > 0 && !slices.Contains(
		route.Spec.EventTypes,
		kargoapi.EventType(evt.Reason),
	) {
		return false
	}
	if len(route.Spec.Stages) > 0 {
		stage := evt.Annotations[kargoapi.AnnotationKeyEventStageName]
		if stage == "" || !slices.Contains(route.Spec.Stages, stage) {
			return false
		}
	}
	return true
}

// eventTime returns the most meaningful timestamp available on the given
// Kubernetes Event.
func eventTime(evt *corev1.Event) time.Time {
	if !evt.LastTimestamp.IsZero() {
		return evt.LastTimestamp.Time
	}
	return evt.CreationTimestamp.Time
}
