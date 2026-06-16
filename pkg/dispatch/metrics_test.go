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

// Not parallel: asserts on the package-level promotions/freights/verifications
// counters, using channel and event names unique to this test to isolate it.
func TestPromotionFreightMetrics(t *testing.T) {
	// A successful Promotion delivered to two channels must increment the
	// promotions counter exactly once, with result derived from the event
	// type and stage taken from the event's annotations.
	promoEvent := newTestEvent(func(e *corev1.Event) {
		e.Name = "promo-metric-event"
		e.UID = "promo-metric-uid"
		e.Reason = string(kargoapi.EventTypePromotionSucceeded)
	})
	freightEvent := newTestEvent(func(e *corev1.Event) {
		e.Name = "freight-metric-event"
		e.UID = "freight-metric-uid"
		e.Reason = string(kargoapi.EventTypeFreightApproved)
	})
	verificationEvent := newTestEvent(func(e *corev1.Event) {
		e.Name = "verification-metric-event"
		e.UID = "verification-metric-uid"
		e.Reason = string(kargoapi.EventTypeFreightVerificationFailed)
	})
	c := fake.NewClientBuilder().
		WithScheme(newTestScheme(t)).
		WithObjects(
			promoEvent,
			freightEvent,
			verificationEvent,
			newTestRouter("pf-router", []string{"pf-a", "pf-b"}),
			newTestChannel("pf-a"),
			newTestChannel("pf-b"),
		).
		Build()
	r := newReconciler(c, c, ReconcilerConfigFromEnv())
	r.newSinkFn = (&fakeSinkFactory{}).new
	r.nowFn = func() time.Time { return testNow }

	reconcile := func(name string) {
		_, err := r.Reconcile(context.Background(), ctrl.Request{
			NamespacedName: types.NamespacedName{Namespace: testProject, Name: name},
		})
		require.NoError(t, err)
	}
	reconcile("promo-metric-event")
	// Reconciling the same event again (e.g. a resync) must not double count.
	reconcile("promo-metric-event")
	reconcile("freight-metric-event")
	reconcile("verification-metric-event")

	require.Equal(t, float64(1), testutil.ToFloat64(
		promotionsTotal.WithLabelValues(testProject, "prod", resultSuccess),
	))
	require.Equal(t, float64(1), testutil.ToFloat64(
		freightsTotal.WithLabelValues(testProject, "prod", resultApproved),
	))
	require.Equal(t, float64(1), testutil.ToFloat64(
		verificationsTotal.WithLabelValues(testProject, "prod", resultFailure),
	))
}
