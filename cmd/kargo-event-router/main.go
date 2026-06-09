package main

import (
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	kargoapi "github.com/akuity/kargo/api/v1alpha1"

	"github.com/shamsalmon/kargo-event-router/api/v1alpha1"
	"github.com/shamsalmon/kargo-event-router/pkg/dispatch"
)

type serverConfig struct {
	MetricsBindAddress     string `envconfig:"METRICS_BIND_ADDRESS" default:"0"`
	HealthProbeBindAddress string `envconfig:"HEALTH_PROBE_BIND_ADDRESS" default:":8081"`
	EnableLeaderElection   bool   `envconfig:"ENABLE_LEADER_ELECTION" default:"false"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctrl.SetLogger(zap.New())
	logger := ctrl.Log.WithName("setup")

	cfg := serverConfig{}
	envconfig.MustProcess("", &cfg)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("error adding Kubernetes core API to scheme: %w", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("error adding kargo-event-router API to scheme: %w", err)
	}

	mgr, err := ctrl.NewManager(
		ctrl.GetConfigOrDie(),
		ctrl.Options{
			Scheme:                 scheme,
			Metrics:                server.Options{BindAddress: cfg.MetricsBindAddress},
			HealthProbeBindAddress: cfg.HealthProbeBindAddress,
			LeaderElection:         cfg.EnableLeaderElection,
			LeaderElectionID:       "kargo-event-router.io",
			Cache: cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					// Only Events recorded for Kargo resources are cached.
					// The field selector is applied server-side, so the
					// informer never receives unrelated Events.
					&corev1.Event{}: {
						Field: fields.OneTermEqualSelector(
							"involvedObject.apiVersion",
							kargoapi.GroupVersion.String(),
						),
					},
				},
			},
		},
	)
	if err != nil {
		return fmt.Errorf("error initializing controller manager: %w", err)
	}

	if err = dispatch.SetupReconcilerWithManager(
		mgr,
		dispatch.ReconcilerConfigFromEnv(),
	); err != nil {
		return fmt.Errorf("error setting up event dispatch reconciler: %w", err)
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("error setting up health check: %w", err)
	}
	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("error setting up ready check: %w", err)
	}

	logger.Info("starting kargo-event-router")
	if err = mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("error starting controller manager: %w", err)
	}
	return nil
}
