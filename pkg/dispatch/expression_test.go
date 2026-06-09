package dispatch

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"
)

func TestEvalWhen(t *testing.T) {
	t.Parallel()

	testEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kargo-demo",
			Annotations: map[string]string{
				kargoapi.AnnotationKeyEventStageName:     "production",
				kargoapi.AnnotationKeyEventFreightAlias:  "salty-seahorse",
				kargoapi.AnnotationKeyEventPromotionName: "prod.01jx.abc",
			},
		},
		Reason:  string(kargoapi.EventTypePromotionErrored),
		Message: "something broke",
	}

	testCases := []struct {
		name   string
		when   string
		assert func(*testing.T, bool, error)
	}{
		{
			name: "matching stage comparison",
			when: `event.stageName == 'production'`,
			assert: func(t *testing.T, matched bool, err error) {
				require.NoError(t, err)
				require.True(t, matched)
			},
		},
		{
			name: "non-matching stage comparison",
			when: `event.stageName == 'uat'`,
			assert: func(t *testing.T, matched bool, err error) {
				require.NoError(t, err)
				require.False(t, matched)
			},
		},
		{
			name: "compound expression over type, project, and message",
			when: `event.type == 'PromotionErrored' && event.project == 'kargo-demo' && event.message contains 'broke'`,
			assert: func(t *testing.T, matched bool, err error) {
				require.NoError(t, err)
				require.True(t, matched)
			},
		},
		{
			name: "string functions are available",
			when: `hasPrefix(event.promotionName, 'prod.')`,
			assert: func(t *testing.T, matched bool, err error) {
				require.NoError(t, err)
				require.True(t, matched)
			},
		},
		{
			name: "unparseable expression",
			when: `this is not an expression`,
			assert: func(t *testing.T, _ bool, err error) {
				require.Error(t, err)
			},
		},
		{
			name: "non-boolean result",
			when: `event.stageName`,
			assert: func(t *testing.T, _ bool, err error) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "did not evaluate to a boolean")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			matched, err := evalWhen(testCase.when, testEvent)
			testCase.assert(t, matched, err)
		})
	}
}

func TestCamelCase(t *testing.T) {
	t.Parallel()
	testCases := map[string]string{
		"stage-name":              "stageName",
		"freight-alias":           "freightAlias",
		"verification-start-time": "verificationStartTime",
		"actor":                   "actor",
		"":                        "",
	}
	for in, want := range testCases {
		require.Equal(t, want, camelCase(in))
	}
}
