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
	"github.com/shamsalmon/kargo-event-router/pkg/payload"
	"github.com/shamsalmon/kargo-event-router/pkg/sink"
)

const testProject = "kargo-demo"

var testNow = time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

// fakeSinkFactory records deliveries and can simulate failures per channel.
type fakeSinkFactory struct {
	sends        []fakeSend
	errByChannel map[string]error
}

type fakeSend struct {
	channel    string
	secretData map[string][]byte
	event      *payload.CloudEvent
}

func (f *fakeSinkFactory) new(
	channel *v1alpha1.MessageChannel,
	secretData map[string][]byte,
	_ time.Duration,
) (sink.Sink, error) {
	return &fakeSink{
		factory:    f,
		channel:    channel.Name,
		secretData: secretData,
	}, nil
}

type fakeSink struct {
	factory    *fakeSinkFactory
	channel    string
	secretData map[string][]byte
}

func (s *fakeSink) Send(_ context.Context, evt *payload.CloudEvent) error {
	if err := s.factory.errByChannel[s.channel]; err != nil {
		return err
	}
	s.factory.sends = append(s.factory.sends, fakeSend{
		channel:    s.channel,
		secretData: s.secretData,
		event:      evt,
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

func newTestChannel(name string) *v1alpha1.MessageChannel {
	return &v1alpha1.MessageChannel{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testProject},
		Spec: v1alpha1.MessageChannelSpec{
			Webhook: &v1alpha1.WebhookChannelConfig{
				URL: "https://hooks.example.com/" + name,
			},
		},
	}
}

func newTestRouter(
	name string,
	channels []string,
	modifiers ...func(*v1alpha1.EventRouter),
) *v1alpha1.EventRouter {
	router := &v1alpha1.EventRouter{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testProject},
	}
	for _, channel := range channels {
		router.Spec.Channels = append(
			router.Spec.Channels,
			v1alpha1.ChannelReference{Name: channel, Kind: "MessageChannel"},
		)
	}
	for _, modify := range modifiers {
		modify(router)
	}
	return router
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		objects      []client.Object
		errByChannel map[string]error
		assert       func(*testing.T, client.Client, *fakeSinkFactory, error)
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
				newTestRouter("router-a", []string{"channel-a"}),
				newTestChannel("channel-a"),
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
				newTestRouter("router-a", []string{"channel-a"}),
				newTestChannel("channel-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name:    "no routers",
			objects: []client.Object{newTestEvent()},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "router with non-matching type is skipped",
			objects: []client.Object{
				newTestEvent(),
				newTestRouter(
					"router-a",
					[]string{"channel-a"},
					func(router *v1alpha1.EventRouter) {
						router.Spec.Types = []kargoapi.EventType{
							kargoapi.EventTypeFreightVerificationFailed,
						}
					},
				),
				newTestChannel("channel-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "router with non-matching when expression is skipped",
			objects: []client.Object{
				newTestEvent(),
				newTestRouter(
					"router-a",
					[]string{"channel-a"},
					func(router *v1alpha1.EventRouter) {
						router.Spec.When = `event.stageName == 'production'`
					},
				),
				newTestChannel("channel-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "router with broken when expression is skipped without error",
			objects: []client.Object{
				newTestEvent(),
				newTestRouter(
					"router-a",
					[]string{"channel-a"},
					func(router *v1alpha1.EventRouter) {
						router.Spec.When = `this is not an expression`
					},
				),
				newTestChannel("channel-a"),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "matching router delivers to all channels and records them",
			objects: []client.Object{
				newTestEvent(),
				newTestRouter(
					"router-a",
					[]string{"channel-a", "channel-b"},
					func(router *v1alpha1.EventRouter) {
						router.Spec.Types = []kargoapi.EventType{
							kargoapi.EventTypePromotionFailed,
						}
						router.Spec.When = `event.stageName == 'prod'`
					},
				),
				newTestChannel("channel-a"),
				newTestChannel("channel-b"),
			},
			assert: func(
				t *testing.T, c client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Len(t, f.sends, 2)
				require.Equal(t, "channel-a", f.sends[0].channel)
				require.Equal(t, "channel-b", f.sends[1].channel)
				require.Equal(
					t,
					"io.akuity.kargo.promotion-failed",
					f.sends[0].event.Type,
				)
				evt := &corev1.Event{}
				require.NoError(t, c.Get(
					context.Background(),
					client.ObjectKey{Namespace: testProject, Name: "test-event"},
					evt,
				))
				require.Equal(
					t,
					"router-a/channel-a,router-a/channel-b",
					evt.Annotations[annotationKeyRoutedTo],
				)
			},
		},
		{
			name: "already-delivered pair is not delivered to again",
			objects: []client.Object{
				newTestEvent(func(evt *corev1.Event) {
					evt.Annotations[annotationKeyRoutedTo] = "router-a/channel-a"
				}),
				newTestRouter("router-a", []string{"channel-a"}),
				newTestChannel("channel-a"),
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
				newTestRouter("router-a", []string{"channel-a", "channel-b"}),
				newTestChannel("channel-a"),
				newTestChannel("channel-b"),
			},
			errByChannel: map[string]error{
				"channel-a": errors.New("endpoint down"),
			},
			assert: func(
				t *testing.T, c client.Client, f *fakeSinkFactory, err error,
			) {
				require.Error(t, err)
				require.Contains(t, err.Error(), "channel-a")
				require.Len(t, f.sends, 1)
				require.Equal(t, "channel-b", f.sends[0].channel)
				evt := &corev1.Event{}
				require.NoError(t, c.Get(
					context.Background(),
					client.ObjectKey{Namespace: testProject, Name: "test-event"},
					evt,
				))
				require.Equal(
					t,
					"router-a/channel-b",
					evt.Annotations[annotationKeyRoutedTo],
				)
			},
		},
		{
			name: "missing MessageChannel fails delivery",
			objects: []client.Object{
				newTestEvent(),
				newTestRouter("router-a", []string{"does-not-exist"}),
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.Error(t, err)
				require.Empty(t, f.sends)
			},
		},
		{
			name: "channel Secret is resolved and passed to the sink",
			objects: []client.Object{
				newTestEvent(),
				newTestRouter("router-a", []string{"channel-a"}),
				&v1alpha1.MessageChannel{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "channel-a",
						Namespace: testProject,
					},
					Spec: v1alpha1.MessageChannelSpec{
						Slack: &v1alpha1.SlackChannelConfig{
							SecretRef: corev1.LocalObjectReference{
								Name: "test-secret",
							},
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: testProject,
					},
					Data: map[string][]byte{
						sink.SecretKeySlackWebhookURL: []byte("https://hooks.slack.com/services/x"),
					},
				},
			},
			assert: func(
				t *testing.T, _ client.Client, f *fakeSinkFactory, err error,
			) {
				require.NoError(t, err)
				require.Len(t, f.sends, 1)
				require.Equal(
					t,
					[]byte("https://hooks.slack.com/services/x"),
					f.sends[0].secretData[sink.SecretKeySlackWebhookURL],
				)
			},
		},
		{
			name: "missing Secret fails delivery",
			objects: []client.Object{
				newTestEvent(),
				newTestRouter("router-a", []string{"channel-a"}),
				&v1alpha1.MessageChannel{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "channel-a",
						Namespace: testProject,
					},
					Spec: v1alpha1.MessageChannelSpec{
						Slack: &v1alpha1.SlackChannelConfig{
							SecretRef: corev1.LocalObjectReference{
								Name: "does-not-exist",
							},
						},
					},
				},
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
				newTestRouter("router-a", []string{"channel-a"}),
				newTestChannel("channel-a"),
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
			factory := &fakeSinkFactory{errByChannel: testCase.errByChannel}
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

func TestRouterMatches(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		router  *v1alpha1.EventRouter
		event   *corev1.Event
		matches bool
	}{
		{
			name:    "no filters matches everything",
			router:  newTestRouter("router-a", []string{"channel-a"}),
			event:   newTestEvent(),
			matches: true,
		},
		{
			name: "types and when both match",
			router: newTestRouter(
				"router-a",
				[]string{"channel-a"},
				func(router *v1alpha1.EventRouter) {
					router.Spec.Types = []kargoapi.EventType{
						kargoapi.EventTypePromotionCreated,
						kargoapi.EventTypePromotionFailed,
					}
					router.Spec.When = `event.stageName == 'prod' && event.promotionName != ''`
				},
			),
			event:   newTestEvent(),
			matches: true,
		},
		{
			name: "when references a field the event does not have",
			router: newTestRouter(
				"router-a",
				[]string{"channel-a"},
				func(router *v1alpha1.EventRouter) {
					router.Spec.When = `event.freightAlias == 'salty-seahorse'`
				},
			),
			event:   newTestEvent(),
			matches: false,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			matched, err := routerMatches(testCase.router, testCase.event)
			require.NoError(t, err)
			require.Equal(t, testCase.matches, matched)
		})
	}
}
