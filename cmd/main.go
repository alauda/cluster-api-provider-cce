/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	cgrecord "k8s.io/client-go/tools/record"
	"k8s.io/component-base/logs"
	v1 "k8s.io/component-base/logs/api/v1"
	_ "k8s.io/component-base/logs/json/register"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	expclusterv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	infrastructurev1beta1 "github.com/alauda/cluster-api-provider-cce/api/v1beta1"
	ccecontroller "github.com/alauda/cluster-api-provider-cce/internal/controller"
	"github.com/alauda/cluster-api-provider-cce/pkg/logger"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = logger.NewLogger(ctrl.Log.WithName("setup"))
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(expclusterv1.AddToScheme(scheme))
	utilruntime.Must(infrastructurev1beta1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

var (
	metricsAddr             string
	enableLeaderElection    bool
	leaderElectionNamespace string
	probeAddr               string
	webhookPort             int
	webhookCertDir          string
	waitInfraPeriod         time.Duration
	syncPeriod              time.Duration
	cceClusterConcurrency   int
	watchFilterValue        string
	logOptions              = logs.NewOptions()
)

func initFlags(fs *pflag.FlagSet) {
	fs.StringVar(
		&metricsAddr,
		"metrics-bind-address",
		":8080",
		"The address the metric endpoint binds to.",
	)

	flag.StringVar(
		&probeAddr,
		"health-probe-bind-address",
		":8081",
		"The address the probe endpoint binds to.",
	)

	flag.BoolVar(
		&enableLeaderElection,
		"leader-elect",
		false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.",
	)

	fs.IntVar(&webhookPort,
		"webhook-port",
		9443,
		"Webhook Server port.",
	)

	fs.StringVar(
		&leaderElectionNamespace,
		"leader-elect-namespace",
		"",
		"Namespace that the controller performs leader election in. If unspecified, the controller will discover which namespace it is running in.",
	)

	fs.DurationVar(&syncPeriod,
		"sync-period",
		10*time.Minute,
		fmt.Sprintf("The minimum interval at which watched resources are reconciled. If EKS is enabled the maximum allowed is %s", "10m"),
	)

	fs.StringVar(&webhookCertDir, "webhook-cert-dir", "/tmp/k8s-webhook-server/serving-certs/",
		"Webhook cert dir, only used when webhook-port is specified.")

	fs.IntVar(&cceClusterConcurrency,
		"ccecluster-concurrency",
		5,
		"Number of CCEClusters to process simultaneously",
	)

	fs.StringVar(
		&watchFilterValue,
		"watch-filter",
		"",
		fmt.Sprintf("Label value that the controller watches to reconcile cluster-api objects. Label key is always %s. If unspecified, the controller watches for all cluster-api objects.", clusterv1.WatchLabel),
	)

	fs.DurationVar(&waitInfraPeriod,
		"wait-infra-period",
		1*time.Minute,
		"The minimum interval at which reconcile process wait for infrastructure to be ready.",
	)

	logs.AddFlags(fs, logs.SkipLoggingConfigurationFlags())
	v1.AddFlags(logOptions, fs)
}

func main() {
	rand.Seed(time.Now().UnixNano())
	initFlags(pflag.CommandLine)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	if err := v1.ValidateAndApply(logOptions, nil); err != nil {
		setupLog.Error(err, "unable to validate and apply log options")
		os.Exit(1)
	}
	ctrl.SetLogger(klog.Background())

	// Machine and cluster operations can create enough events to trigger the event recorder spam filter
	// Setting the burst size higher ensures all events will be recorded and submitted to the API
	broadcaster := cgrecord.NewBroadcasterWithCorrelatorOptions(cgrecord.CorrelatorOptions{
		BurstSize: 100,
	})

	// Setup the context that's going to be used in controllers and for the manager.
	ctx := ctrl.SetupSignalHandler()

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "cluster-api-provider-cce-controller"
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                     scheme,
		MetricsBindAddress:         metricsAddr,
		HealthProbeBindAddress:     probeAddr,
		LeaderElection:             enableLeaderElection,
		LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
		LeaderElectionID:           "5053f7c0.cluster.x-k8s.io",
		LeaderElectionNamespace:    leaderElectionNamespace,
		SyncPeriod:                 &syncPeriod,
		EventBroadcaster:           broadcaster,
		Port:                       webhookPort,
		CertDir:                    webhookCertDir,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	setupLog.Info("enabling CCE controllers and webhooks")

	setupLog.Debug("enabling CCE control plane controller")
	if err = (&ccecontroller.CCEManagedControlPlaneReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		WatchFilterValue: watchFilterValue,
		WaitInfraPeriod:  waitInfraPeriod,
	}).SetupWithManager(ctx, mgr, controller.Options{MaxConcurrentReconciles: cceClusterConcurrency, RecoverPanic: pointer.Bool(true)}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CCEManagedControlPlane")
		os.Exit(1)
	}

	setupLog.Debug("enabling CCE managed cluster controller")
	if err = (&ccecontroller.CCEManagedClusterReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		WatchFilterValue: watchFilterValue,
		WaitInfraPeriod:  waitInfraPeriod,
	}).SetupWithManager(ctx, mgr, controller.Options{MaxConcurrentReconciles: cceClusterConcurrency, RecoverPanic: pointer.Bool(true)}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CCEManagedCluster")
		os.Exit(1)
	}

	setupLog.Debug("enabling CCE managed machine pool controller")
	if err = (&ccecontroller.CCEManagedMachinePoolReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		WatchFilterValue: watchFilterValue,
		WaitInfraPeriod:  waitInfraPeriod,
	}).SetupWithManager(ctx, mgr, controller.Options{MaxConcurrentReconciles: cceClusterConcurrency, RecoverPanic: pointer.Bool(true)}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CCEManagedMachinePool")
		os.Exit(1)
	}

	// if err = (&infrastructurev1beta1.CCEManagedCluster{}).SetupWebhookWithManager(mgr); err != nil {
	// 	setupLog.Error(err, "unable to create webhook", "webhook", "CCEManagedCluster")
	// 	os.Exit(1)
	// }
	// if err = (&infrastructurev1beta1.CCEManagedControlPlane{}).SetupWebhookWithManager(mgr); err != nil {
	// 	setupLog.Error(err, "unable to create webhook", "webhook", "CCEManagedControlPlane")
	// 	os.Exit(1)
	// }
	// if err = (&infrastructurev1beta1.CCEManagedMachinePool{}).SetupWebhookWithManager(mgr); err != nil {
	// 	setupLog.Error(err, "unable to create webhook", "webhook", "CCEManagedMachinePool")
	// 	os.Exit(1)
	// }
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
