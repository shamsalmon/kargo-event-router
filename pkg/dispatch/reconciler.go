// Package dispatch watches Kubernetes Events recorded by Kargo and delivers
// them to the channels referenced by EventRouter resources in the same
// namespace (i.e. the same Kargo Project).
package dispatch

import (
	"context"
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

// annotationKeyRoutedTo is set on Kubernetes Events to record the
// router/channel pairs to which the event has already been delivered. This
// is what makes delivery idempotent across retries and controller restarts.
const annotationKeyRoutedTo = "kargo-event-router.io/routed-to"

// ReconcilerConfig represents configuration for the event dispatch
// reconciler.
type ReconcilerConfig struct {
	// MaxEventAge is the maximum age an event may have to still be eligible
	// for delivery. This prevents replaying old events when the controller
	// starts (or restarts) and resyncs.
	MaxEventAge time.Duration `envconfig:"MAX_EVENT_AGE" default:"30m"`
	// SendTimeout is the per-request timeout for deliveries to channels.
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
	newSinkFn func(
		channel *v1alpha1.MessageChannel,
		secretData map[string][]byte,
		timeout time.Duration,
	) (sink.Sink, error)
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
		newSinkFn: sink.New,
		nowFn:     time.Now,
	}
}

// SetupReconcilerWithManager initializes the event dispatch reconciler and
// registers it with the provided Manager.
//
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups=kargo-event-router.io,resources=eventrouters;messagechannels,verbs=get;list;watch
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

	routers := &v1alpha1.EventRouterList{}
	if err := r.client.List(
		ctx,
		routers,
		client.InNamespace(evt.Namespace),
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("error listing EventRouters: %w", err)
	}

	// Collect the router/channel pairs the event must still be delivered to.
	delivered := routedTo(evt)
	var pending []delivery
	for i := range routers.Items {
		router := &routers.Items[i]
		matched, err := routerMatches(router, evt)
		if err != nil {
			// A broken when expression cannot be fixed by retrying. Log it
			// and move on; it will be re-evaluated when its next event
			// occurs.
			logger.Error(err, "error matching EventRouter", "router", router.Name)
			continue
		}
		if !matched {
			continue
		}
		for _, channel := range router.Spec.Channels {
			d := delivery{router: router.Name, channel: channel.Name}
			if !slices.Contains(delivered, d.key()) {
				pending = append(pending, d)
			}
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

	// Deliver to each pending channel, marking the event after each
	// successful delivery so a retry triggered by one channel's failure
	// does not re-deliver to channels that already succeeded.
	var errs []error
	for _, d := range pending {
		if err = r.deliver(ctx, evt.Namespace, d.channel, ce); err != nil {
			errs = append(
				errs,
				fmt.Errorf("error delivering to channel %q: %w", d.channel, err),
			)
			continue
		}
		logger.Info(
			"delivered event",
			"reason", evt.Reason,
			"router", d.router,
			"channel", d.channel,
		)
		if err = r.markRouted(ctx, evt, d.key()); err != nil {
			errs = append(errs, fmt.Errorf(
				"error marking event as routed to %q: %w", d.key(), err,
			))
		}
	}
	return ctrl.Result{}, errors.Join(errs...)
}

// delivery identifies one router/channel pair an event is delivered to.
type delivery struct {
	router  string
	channel string
}

// key returns the representation of the delivery recorded in the routed-to
// annotation. Kubernetes resource names cannot contain "/", so the pair is
// unambiguous.
func (d delivery) key() string {
	return d.router + "/" + d.channel
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

// deliver sends the given event to the named MessageChannel.
func (r *reconciler) deliver(
	ctx context.Context,
	namespace string,
	channelName string,
	ce *payload.CloudEvent,
) error {
	channel := &v1alpha1.MessageChannel{}
	if err := r.client.Get(
		ctx,
		client.ObjectKey{Namespace: namespace, Name: channelName},
		channel,
	); err != nil {
		return fmt.Errorf("error getting MessageChannel: %w", err)
	}
	var secretData map[string][]byte
	if ref := channelSecretRef(channel); ref != nil {
		// Secrets in project namespaces are deliberately read with the
		// uncached reader so the controller does not have to watch and cache
		// Secrets cluster-wide.
		secret := &corev1.Secret{}
		if err := r.apiReader.Get(
			ctx,
			client.ObjectKey{Namespace: namespace, Name: ref.Name},
			secret,
		); err != nil {
			return fmt.Errorf("error getting Secret %q: %w", ref.Name, err)
		}
		secretData = secret.Data
	}
	s, err := r.newSinkFn(channel, secretData, r.cfg.SendTimeout)
	if err != nil {
		return err
	}
	return s.Send(ctx, ce)
}

// channelSecretRef returns the reference to the Secret the given
// MessageChannel depends on, if any.
func channelSecretRef(
	channel *v1alpha1.MessageChannel,
) *corev1.LocalObjectReference {
	switch {
	case channel.Spec.Webhook != nil:
		return channel.Spec.Webhook.SecretRef
	case channel.Spec.Slack != nil:
		return &channel.Spec.Slack.SecretRef
	}
	return nil
}

// markRouted records on the event that it has been delivered to the given
// router/channel pair.
func (r *reconciler) markRouted(
	ctx context.Context,
	evt *corev1.Event,
	deliveryKey string,
) error {
	patch := client.MergeFrom(evt.DeepCopy())
	if evt.Annotations == nil {
		evt.Annotations = map[string]string{}
	}
	evt.Annotations[annotationKeyRoutedTo] = strings.Join(
		append(routedTo(evt), deliveryKey),
		",",
	)
	return r.client.Patch(ctx, evt, patch)
}

// routedTo returns the router/channel pairs to which the given event has
// already been delivered.
func routedTo(evt *corev1.Event) []string {
	var keys []string
	for _, key := range strings.Split(
		evt.Annotations[annotationKeyRoutedTo], ",",
	) {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// routerMatches returns true if the given EventRouter applies to the given
// event.
func routerMatches(
	router *v1alpha1.EventRouter,
	evt *corev1.Event,
) (bool, error) {
	if len(router.Spec.Types) > 0 && !slices.Contains(
		router.Spec.Types,
		kargoapi.EventType(evt.Reason),
	) {
		return false, nil
	}
	if router.Spec.When == "" {
		return true, nil
	}
	return evalWhen(router.Spec.When, evt)
}

// eventTime returns the most meaningful timestamp available on the given
// Kubernetes Event.
func eventTime(evt *corev1.Event) time.Time {
	if !evt.LastTimestamp.IsZero() {
		return evt.LastTimestamp.Time
	}
	return evt.CreationTimestamp.Time
}
