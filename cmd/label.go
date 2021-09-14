package cmd

import (
	"context"
	"time"

	"github.com/StatCan/namespace-controller/pkg/controllers/namespaces"
	"github.com/StatCan/namespace-controller/pkg/signals"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

var labelCmd = &cobra.Command{
	Use:   "label",
	Short: "Propagate labels from namespace to certain resources.",
	Long: `Propagate labels from namespace to certain resources.
* Propagate labels for finance
	`,
	Run: func(cmd *cobra.Command, args []string) {
		// Setup signals so we can shutdown cleanly
		stopCh := signals.SetupSignalHandler()

		// Create Kubernetes config
		cfg, err := clientcmd.BuildConfigFromFlags(apiserver, kubeconfig)
		if err != nil {
			klog.Fatalf("error building kubeconfig: %v", err)
		}

		kubeClient, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			klog.Fatalf("Error building kubernetes clientset: %s", err.Error())
		}

		// Setup informers
		kubeInformerFactory := kubeinformers.NewSharedInformerFactory(kubeClient, time.Minute*5)

		// Pod informers
		podInformer := kubeInformerFactory.Core().V1().Pods()
		podLister := podInformer.Lister()

		// PV informers
		pvcInformer := kubeInformerFactory.Core().V1().PersistentVolumeClaims()
		pvcLister := pvcInformer.Lister()

		// Setup controller
		controller := namespaces.NewController(
			kubeInformerFactory.Core().V1().Namespaces(),
			func(namespace *corev1.Namespace) error {
				// Skip 'control-plane' namespaces
				if _, ok := namespace.ObjectMeta.Labels["control-plane"]; ok {
					klog.Infof("skipping namespace <%v> as it is a cluster control plane namespace", namespace.Name)
					return nil
				}

				// Propagate 'workload-id' to pod resources
				if _, ok := namespace.ObjectMeta.Labels["finance.statcan.gc.ca/workload-id"]; ok {
					klog.Infof("propagating namespace <%v> workload-id labels to pod resources", namespace.Name)
					namespacePods, err := podLister.Pods(namespace.Name).List(labels.Everything())
					if err != nil {
						klog.Infof("failed to list pods under namespace %s", namespace.Name)
						return nil
					}

					for _, pod := range namespacePods {
						existingLabels := pod.Labels
						existingLabels["finance.statcan.gc.ca/workload-id"] = namespace.ObjectMeta.Labels["workload-id"]
						pod.SetLabels(existingLabels)
						_, err = kubeClient.CoreV1().Pods(pod.Namespace).Update(context.Background(), pod, metav1.UpdateOptions{})
						if err != nil {
							return err
						}
					}
				}

				// Propagate 'workload-id' to pvc resources
				if _, ok := namespace.ObjectMeta.Labels["finance.statcan.gc.ca/workload-id"]; ok {
					klog.Infof("propagating namespace <%v> workload-id labels to pvc resources", namespace.Name)
					namespacePvcs, err := pvcLister.List(labels.Everything())
					if err != nil {
						klog.Infof("failed to list pvc under namespace %s", namespace.Name)
						return nil
					}

					for _, pvc := range namespacePvcs {
						existingLabels := pvc.Labels
						existingLabels["finance.statcan.gc.ca/workload-id"] = namespace.ObjectMeta.Labels["workload-id"]
						pvc.SetLabels(existingLabels)
						_, err = kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Update(context.Background(), pvc, metav1.UpdateOptions{})
						if err != nil {
							return err
						}
					}
				}

				return nil
			},
		)

		// Start informers
		kubeInformerFactory.Start(stopCh)

		// Wait for caches
		klog.Info("Waiting for informer caches to sync")
		if ok := cache.WaitForCacheSync(stopCh, podInformer.Informer().HasSynced, pvcInformer.Informer().HasSynced); !ok {
			klog.Fatalf("failed to wait for caches to sync")
		}

		// Run the controller
		if err = controller.Run(2, stopCh); err != nil {
			klog.Fatalf("error running controller: %v", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(labelCmd)
}
