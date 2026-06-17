package dispatch

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

func newInitTestScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	require.NoError(t, kargoapi.AddToScheme(scheme))
	return scheme
}

func reconcileStage(t *testing.T, r *metricsInitializer, namespace, name string) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
	})
	require.NoError(t, err)
}

// countSeriesForProject returns how many child series of vec carry the given
// value for the "project" label. The package-level counters are shared across
// tests, so filtering by a test-unique project keeps the assertions isolated.
func countSeriesForProject(vec *prometheus.CounterVec, project string) int {
	ch := make(chan prometheus.Metric, 256)
	vec.Collect(ch)
	close(ch)
	n := 0
	for m := range ch {
		var metric dto.Metric
		if err := m.Write(&metric); err != nil {
			continue
		}
		for _, l := range metric.Label {
			if l.GetName() == "project" && l.GetValue() == project {
				n++
			}
		}
	}
	return n
}

// Not parallel: asserts on the package-level events counter, using project and
// stage names unique to this test to isolate it from other tests.
func TestMetricsInitializer(t *testing.T) {
	stageA := &kargoapi.Stage{
		ObjectMeta: metav1.ObjectMeta{Name: "init-stage-a", Namespace: "init-proj"},
	}
	stageB := &kargoapi.Stage{
		ObjectMeta: metav1.ObjectMeta{Name: "init-stage-b", Namespace: "init-proj"},
	}
	c := fake.NewClientBuilder().
		WithScheme(newInitTestScheme(t)).
		WithObjects(stageA, stageB).
		Build()
	r := &metricsInitializer{client: c}

	reconcileStage(t, r, "init-proj", "init-stage-a")
	reconcileStage(t, r, "init-proj", "init-stage-b")

	// Each Stage gets a 0 series for every event type, labeled with its
	// namespace as the project.
	require.Equal(t, 2*len(allEventTypes),
		countSeriesForProject(eventsTotal, "init-proj"))
	require.Equal(t, float64(0), testutil.ToFloat64(
		eventsTotal.WithLabelValues(
			"init-proj", "init-stage-a", string(kargoapi.EventTypePromotionSucceeded),
		),
	))
}

// Reconciling a Stage that no longer exists must not create any series.
func TestMetricsInitializerMissingStage(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(newInitTestScheme(t)).Build()
	r := &metricsInitializer{client: c}

	reconcileStage(t, r, "gone-proj", "gone-stage")

	require.Equal(t, 0, countSeriesForProject(eventsTotal, "gone-proj"))
}
