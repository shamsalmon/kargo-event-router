package payload

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
	libevent "github.com/akuity/kargo/pkg/event"
)

func TestNew(t *testing.T) {
	t.Parallel()

	testTime := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	testCases := []struct {
		name   string
		event  *corev1.Event
		assert func(*testing.T, *CloudEvent, error)
	}{
		{
			name: "promotion failed event",
			event: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kargo-demo",
					UID:       types.UID("test-uid"),
					Annotations: map[string]string{
						kargoapi.AnnotationKeyEventProject:             "kargo-demo",
						kargoapi.AnnotationKeyEventPromotionName:       "test-promotion",
						kargoapi.AnnotationKeyEventStageName:           "prod",
						kargoapi.AnnotationKeyEventPromotionCreateTime: testTime.Format(time.RFC3339),
					},
				},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Promotion",
					Name: "test-promotion",
				},
				Reason:        string(kargoapi.EventTypePromotionFailed),
				Message:       "Promotion Failed",
				LastTimestamp: metav1.Time{Time: testTime},
			},
			assert: func(t *testing.T, ce *CloudEvent, err error) {
				require.NoError(t, err)
				require.Equal(t, "1.0", ce.SpecVersion)
				require.Equal(t, "test-uid", ce.ID)
				require.Equal(t, "kargo/kargo-demo", ce.Source)
				require.Equal(t, "io.akuity.kargo.promotion-failed", ce.Type)
				require.Equal(t, "Promotion/test-promotion", ce.Subject)
				require.Equal(t, testTime, ce.Time)
				data, ok := ce.Data.(*libevent.PromotionFailed)
				require.True(t, ok)
				require.Equal(t, "kargo-demo", data.GetProject())
				require.Equal(t, "test-promotion", data.Promotion.Name)
				require.Equal(t, "prod", data.Promotion.StageName)
				require.Equal(t, "Promotion Failed", data.GetMessage())
			},
		},
		{
			name: "freight verification failed event",
			event: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kargo-demo",
					UID:       types.UID("test-uid"),
					Annotations: map[string]string{
						kargoapi.AnnotationKeyEventProject:           "kargo-demo",
						kargoapi.AnnotationKeyEventFreightName:       "test-freight",
						kargoapi.AnnotationKeyEventFreightCreateTime: testTime.Format(time.RFC3339),
						kargoapi.AnnotationKeyEventStageName:         "prod",
						kargoapi.AnnotationKeyEventAnalysisRunName:   "test-analysis-run",
					},
				},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Freight",
					Name: "test-freight",
				},
				Reason:  string(kargoapi.EventTypeFreightVerificationFailed),
				Message: "Freight verification failed",
			},
			assert: func(t *testing.T, ce *CloudEvent, err error) {
				require.NoError(t, err)
				require.Equal(
					t, "io.akuity.kargo.freight-verification-failed", ce.Type,
				)
				data, ok := ce.Data.(*libevent.FreightVerificationFailed)
				require.True(t, ok)
				require.Equal(t, "test-freight", data.Freight.Name)
				require.NotNil(t, data.AnalysisRunName)
				require.Equal(t, "test-analysis-run", *data.AnalysisRunName)
				require.Equal(t, "Freight verification failed", data.GetMessage())
			},
		},
		{
			name: "unknown event type degrades to generic data",
			event: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kargo-demo",
					UID:       types.UID("test-uid"),
					Annotations: map[string]string{
						kargoapi.AnnotationKeyEventProject: "kargo-demo",
						"unrelated-annotation":             "should-be-excluded",
					},
				},
				InvolvedObject: corev1.ObjectReference{
					Kind: "Stage",
					Name: "prod",
				},
				Reason:  "SomeFutureEventType",
				Message: "something happened",
			},
			assert: func(t *testing.T, ce *CloudEvent, err error) {
				require.NoError(t, err)
				require.Equal(
					t, "io.akuity.kargo.some-future-event-type", ce.Type,
				)
				data, ok := ce.Data.(map[string]any)
				require.True(t, ok)
				require.Equal(t, "something happened", data["message"])
				annotations, ok := data["annotations"].(map[string]string)
				require.True(t, ok)
				require.Contains(t, annotations, kargoapi.AnnotationKeyEventProject)
				require.NotContains(t, annotations, "unrelated-annotation")
			},
		},
		{
			name: "malformed annotations",
			event: &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "kargo-demo",
					UID:       types.UID("test-uid"),
					Annotations: map[string]string{
						kargoapi.AnnotationKeyEventPromotionCreateTime: "not-a-time",
					},
				},
				Reason: string(kargoapi.EventTypePromotionFailed),
			},
			assert: func(t *testing.T, _ *CloudEvent, err error) {
				require.Error(t, err)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			ce, err := New(testCase.event)
			testCase.assert(t, ce, err)
		})
	}
}

func TestKebabCase(t *testing.T) {
	t.Parallel()
	testCases := map[string]string{
		"PromotionFailed":              "promotion-failed",
		"FreightVerificationSucceeded": "freight-verification-succeeded",
		"already-kebab":                "already-kebab",
		"":                             "",
	}
	for in, want := range testCases {
		require.Equal(t, want, kebabCase(in))
	}
}
