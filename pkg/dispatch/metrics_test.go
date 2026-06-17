package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
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

	// The test event carries a stage-name annotation of "prod", which becomes
	// the stage label.
	eventType := string(kargoapi.EventTypePromotionFailed)
	require.Equal(t, float64(1), testutil.ToFloat64(
		deliveriesTotal.WithLabelValues(
			testProject, "prod", "metrics-ok", channelTypeWebhook, eventType, resultSuccess,
		),
	))
	require.Equal(t, float64(1), testutil.ToFloat64(
		deliveriesTotal.WithLabelValues(
			testProject, "prod", "metrics-fail", channelTypeWebhook, eventType, resultError,
		),
	))
}

// Not parallel: asserts on the package-level events counter, using event names
// unique to this test to isolate it from other tests.
func TestEventsMetric(t *testing.T) {
	event := newTestEvent(func(e *corev1.Event) {
		e.Name = "events-metric"
		e.UID = "events-metric-uid"
		e.Reason = string(kargoapi.EventTypePromotionSucceeded)
	})
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(
			event,
			newTestRouter("events-router", []string{"events-a", "events-b"}),
			newTestChannel("events-a"),
			newTestChannel("events-b"),
		).
		Build()
	r := newReconciler(c, c, ReconcilerConfigFromEnv())
	r.newSinkFn = (&fakeSinkFactory{}).new
	r.nowFn = func() time.Time { return testNow }

	reconcile := func() {
		_, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: testProject, Name: "events-metric"},
		})
		require.NoError(t, err)
	}
	reconcile()
	// Reconciling again (e.g. a resync) must not double count, even though the
	// event fanned out to two channels.
	reconcile()

	eventType := string(kargoapi.EventTypePromotionSucceeded)
	require.Equal(t, float64(1), testutil.ToFloat64(
		eventsTotal.WithLabelValues(testProject, "prod", eventType),
	))
}
