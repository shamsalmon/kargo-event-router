package dispatch

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// metricsInitializer pre-initializes the events counter to 0 for every Kargo
// Stage. Making the series exist from startup means rate()/increase() queries
// and dashboards do not have to special-case the gap between the controller
// starting and the first event of a given kind.
type metricsInitializer struct {
	client client.Client
}

// SetupMetricsInitializerWithManager registers a controller that initializes
// the events counter for every Kargo Stage. It is a no-op (registers nothing,
// requires no Stage access) when enabled is false, so the feature is opt-in.
//
// A Stage carries its project: it lives in the namespace of the Kargo Project
// of the same name, and every event the router counts is scoped to a Stage. So
// watching Stages alone yields every (project, stage) pair that needs series,
// and a Stage created later is initialized as soon as its watch event fires.
//
// +kubebuilder:rbac:groups=kargo.akuity.io,resources=stages,verbs=get;list;watch
func SetupMetricsInitializerWithManager(
	mgr manager.Manager,
	enabled bool,
) error {
	if !enabled {
		return nil
	}
	r := &metricsInitializer{client: mgr.GetClient()}
	return ctrl.NewControllerManagedBy(mgr).
		For(&kargoapi.Stage{}).
		Named("metrics_initializer").
		Complete(r)
}

func (r *metricsInitializer) Reconcile(
	ctx context.Context,
	req ctrl.Request,
) (ctrl.Result, error) {
	// Confirm the Stage still exists before creating series for it; a watch
	// event for a since-deleted Stage should not resurrect a 0 series. The
	// Stage's namespace is its Kargo Project (the project label).
	stage := &kargoapi.Stage{}
	if err := r.client.Get(ctx, req.NamespacedName, stage); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	initEventMetrics(stage.Namespace, stage.Name)
	return ctrl.Result{}, nil
}
