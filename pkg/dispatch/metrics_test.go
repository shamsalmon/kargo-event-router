package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

// Not parallel: asserts on the package-level deliveries counter, using
// channel names unique to this test to isolate it from other tests.
func TestDeliveryMetrics(t *testing.T) {
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(
			newTestEvent(),
			newTestRouter(
				"metrics-router",
				[]string{"metrics-ok", "metrics-fail"},
			),
			newTestChannel("metrics-ok"),
			newTestChannel("metrics-fail"),
		).
		Build()
	factory := &fakeSinkFactory{
		errByChannel: map[string]error{
			"metrics-fail": errors.New("endpoint down"),
		},
	}
	r := newReconciler(c, c, ReconcilerConfigFromEnv())
	r.newSinkFn = factory.new
	r.nowFn = func() time.Time { return testNow }
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: testProject,
			Name:      "test-event",
		},
	})
	require.Error(t, err)

	eventType := string(kargoapi.EventTypePromotionFailed)
	require.Equal(t, float64(1), testutil.ToFloat64(
		deliveriesTotal.WithLabelValues(
			testProject, "metrics-ok", channelTypeWebhook, eventType, resultSuccess,
		),
	))
	require.Equal(t, float64(1), testutil.ToFloat64(
		deliveriesTotal.WithLabelValues(
			testProject, "metrics-fail", channelTypeWebhook, eventType, resultError,
		),
	))
}
