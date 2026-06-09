package dispatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"

	"github.com/shamsalmon/kargo-event-router/api/v1alpha1"
	"github.com/shamsalmon/kargo-event-router/pkg/sink"
)

const testProject = "kargo-demo"

var testNow = time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

// fakeSinkFactory records deliveries and can simulate failures per URL.
type fakeSinkFactory struct {
	sends   []fakeSend
	errByURL map[string]error
}

type fakeSend struct {
	url        string
	signingKey []byte
	payload    []byte
}

func (f *fakeSinkFactory) new(
	url string,
	signingKey []byte,
	_ time.Duration,
) sink.Sink {
	return &fakeSink{factory: f, url: url, signingKey: signingKey}
}

type fakeSink struct {
	factory    *fakeSinkFactory
	url        string
	signingKey []byte
}

func (s *fakeSink) Send(_ context.Context, payload []byte) error {
	if err := s.factory.errByURL[s.url]; err != nil {
		return err
	}
	s.factory.sends = append(s.factory.sends, fakeSend{
		url:        s.url,
		signingKey: s.signingKey,
		payload:    payload,
	})
	return nil
}

func newTestScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	return scheme
}

func newTestEvent(modifiers ...func(*corev1.Event)) *corev1.Event {
	evt := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-event",
			Namespace: testProject,
			UID:       types.UID("test-uid"),
			Annotations: map[string]string{
				kargoapi.AnnotationKeyEventProject:             testProject,
				kargoapi.AnnotationKeyEventPromotionName:       "test-promotion",
				kargoapi.AnnotationKeyEventStageName:           "prod",
				kargoapi.AnnotationKeyEventPromotionCreateTime: testNow.Format(time.RFC3339),
			},
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: kargoapi.GroupVersion.String(),
			Kind:       "Promotion",
			Name:       "test-promotion",
		},
		Reason:        string(kargoapi.EventTypePromotionFailed),
		Message:       "Promotion Failed",
		LastTimestamp: metav1.Time{Time: testNow},
	}
	for _, modify := range modifiers {
		modify(evt)
	}
	return evt
}

func newTestRoute(
	name string,
	modifiers ...func(*v1alpha1.EventRoute),
) *v1alpha1.EventRoute {
	route := &v1alpha1.EventRoute{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testProject},
		Spec: v1alpha1.EventRouteSpec{
			Webhook: v1alpha1.WebhookSinkConfig{
				URL: "https://hooks.example.com/" + name,
			},
		},
	}
	for _, modify := range modifiers {
		modify(route)
	}
	return route
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		objects  []client.Object
		errByURL map[string]error
		assert   func(*testing.T, client.Client, *fakeSinkFactory, error)
	}{
		{
			name: "event not found",
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "non-Kargo event is ignored",
			objects: []client.Object{
				newTestEvent(func(evt *corev1.Event) {
					evt.InvolvedObject.APIVersion = "apps/v1"
				}),
				newTestRoute("route-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "event older than max age is ignored",
			objects: []client.Object{
				newTestEvent(func(evt *corev1.Event) {
					evt.LastTimestamp = metav1.Time{Time: testNow.Add(-time.Hour)}
				}),
				newTestRoute("route-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name:    "no routes",
			objects: []client.Object{newTestEvent()},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "route with non-matching event type is skipped",
			objects: []client.Object{
				newTestEvent(),
				newTestRoute("route-a", func(route *v1alpha1.EventRoute) {
					route.Spec.EventTypes = []kargoapi.EventType{
						kargoapi.EventTypeFreightVerificationFailed,
					}
				}),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "route with non-matching stage is skipped",
			objects: []client.Object{
				newTestEvent(),
				newTestRoute("route-a", func(route *v1alpha1.EventRoute) {
					route.Spec.Stages = []string{"uat"}
				}),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "matching route receives the event and delivery is recorded",
			objects: []client.Object{
				newTestEvent(),
				newTestRoute("route-a", func(route *v1alpha1.EventRoute) {
					route.Spec.EventTypes = []kargoapi.EventType{
						kargoapi.EventTypePromotionFailed,
					}
					route.Spec.Stages = []string{"prod"}
				}),
			},
			assert: func(
				t *testing.T, c client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Len(t, f.sends, 1)
				require.Equal(
					t, "https://hooks.example.com/route-a", f.sends[0].url,
				)
				require.Contains(
					t,
					string(f.sends[0].payload),
					"io.akuity.kargo.promotion-failed",
				)
				evt := &corev1.Event{}
				require.NoError(t, c.Get(
					context.Background(),
					client.ObjectKey{Namespace: testProject, Name: "test-event"},
					evt,
				))
				require.Equal(
					t, "route-a", evt.Annotations[annotationKeyRoutedTo],
				)
			},
		},
		{
			name: "already-delivered route is not delivered to again",
			objects: []client.Object{
				newTestEvent(func(evt *corev1.Event) {
					evt.Annotations[annotationKeyRoutedTo] = "route-a"
				}),
				newTestRoute("route-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "partial failure returns an error but records successes",
			objects: []client.Object{
				newTestEvent(),
				newTestRoute("route-a"),
				newTestRoute("route-b"),
			},
			errByURL: map[string]error{
				"https://hooks.example.com/route-a": errors.New("endpoint down"),
			},
			assert: func(
				t *testing.T, c client.Client, f *fakeSinkFactory, err error,
			) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "route-a")
				require.Len(t, f.sends, 1)
				require.Equal(
					t, "https://hooks.example.com/route-b", f.sends[0].url,
				)
				evt := &corev1.Event{}
				require.NoError(t, c.Get(
					context.Background(),
					client.ObjectKey{Namespace: testProject, Name: "test-event"},
					evt,
				))
				require.Equal(
					t, "route-b", evt.Annotations[annotationKeyRoutedTo],
				)
			},
		},
		{
			name: "signing key is read from the referenced Secret",
			objects: []client.Object{
				newTestEvent(),
				newTestRoute("route-a", func(route *v1alpha1.EventRoute) {
					route.Spec.Webhook.SecretRef = &corev1.LocalObjectReference{
						Name: "test-secret",
					}
				}),
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: testProject,
					},
					Data: map[string][]byte{
						signingKeySecretKey: []byte("test-signing-key"),
					},
				},
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Len(t, f.sends, 1)
				require.Equal(
					t, []byte("test-signing-key"), f.sends[0].signingKey,
				)
			},
		},
		{
			name: "missing Secret fails delivery",
			objects: []client.Object{
				newTestEvent(),
				newTestRoute("route-a", func(route *v1alpha1.EventRoute) {
					route.Spec.Webhook.SecretRef = &corev1.LocalObjectReference{
						Name: "does-not-exist",
					}
				}),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.Error(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "malformed event annotations drop the event without error",
			objects: []client.Object{
				newTestEvent(func(evt *corev1.Event) {
					evt.Annotations[kargoapi.AnnotationKeyEventPromotionCreateTime] = "not-a-time"
				}),
				newTestRoute("route-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			c := fake.NewClientBuilder().
				WithScheme(newTestScheme(t)).
				WithObjects(testCase.objects...).
				Build()
			factory := &fakeSinkFactory{errByURL: testCase.errByURL}
			r := newReconciler(c, c, ReconcilerConfigFromEnv())
			r.newSinkFn = factory.new
			r.nowFn = func() time.Time { return testNow }
			_, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: testProject,
					Name:      "test-event",
				},
			})
			testCase.assert(t, c, factory, err)
		})
	}
}

func TestRouteMatches(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		route   *v1alpha1.EventRoute
		event   *corev1.Event
		matches bool
	}{
		{
			name:    "no filters matches everything",
			route:   newTestRoute("route-a"),
			event:   newTestEvent(),
			matches: true,
		},
		{
			name: "stage filter with event lacking a stage annotation",
			route: newTestRoute("route-a", func(route *v1alpha1.EventRoute) {
				route.Spec.Stages = []string{"prod"}
			}),
			event: newTestEvent(func(evt *corev1.Event) {
				delete(evt.Annotations, kargoapi.AnnotationKeyEventStageName)
			}),
			matches: false,
		},
		{
			name: "both filters match",
			route: newTestRoute("route-a", func(route *v1alpha1.EventRoute) {
				route.Spec.EventTypes = []kargoapi.EventType{
					kargoapi.EventTypePromotionFailed,
				}
				route.Spec.Stages = []string{"uat", "prod"}
			}),
			event:   newTestEvent(),
			matches: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(
				t,
				testCase.matches,
				routeMatches(testCase.route, testCase.event),
			)
		})
	}
}
