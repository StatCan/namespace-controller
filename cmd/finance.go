package cmd

import (
	"context"
	"time"

	"github.com/StatCan/namespace-controller/pkg/controllers/namespaces"
	"github.com/StatCan/namespace-controller/pkg/signals"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

var financeCmd = &cobra.Command{
	Use:   "finance",
	Short: "Manage namespace financial information",
	Long: `
Propagate labels from namespace to certain resources (Pods, PVCs) for finance tracking.
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

		// Namespaces informer
		namespaceInformer := kubeInformerFactory.Core().V1().Namespaces()
		namespaceLister := namespaceInformer.Lister()

		// Pod informers
		podInformer := kubeInformerFactory.Core().V1().Pods()
		podLister := podInformer.Lister()

		// PV informers
		pvcInformer := kubeInformerFactory.Core().V1().PersistentVolumeClaims()
		pvcLister := pvcInformer.Lister()

		// Setup controller
		controller := namespaces.NewController(
			namespaceInformer,
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
						if existingLabels["finance.statcan.gc.ca/workload-id"] != namespace.ObjectMeta.Labels["workload-id"] {
							existingLabels["finance.statcan.gc.ca/workload-id"] = namespace.ObjectMeta.Labels["workload-id"]
							pod.SetLabels(existingLabels)
							_, err = kubeClient.CoreV1().Pods(pod.Namespace).Update(context.Background(), pod, metav1.UpdateOptions{})
							if err != nil {
								return err
							}
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
						if existingLabels["finance.statcan.gc.ca/workload-id"] != namespace.ObjectMeta.Labels["workload-id"] {
							existingLabels["finance.statcan.gc.ca/workload-id"] = namespace.ObjectMeta.Labels["workload-id"]
							pvc.SetLabels(existingLabels)
							_, err = kubeClient.CoreV1().PersistentVolumeClaims(pvc.Namespace).Update(context.Background(), pvc, metav1.UpdateOptions{})
							if err != nil {
								return err
							}
						}
					}
				}

				return nil
			},
		)

		// Setup callback handlers when new objects are created
		//   Upon an Add or Update event, we will trigger a resync
		//   of the relevant namespace in order to ensure the labels
		//   are correctly applied.
		metaAccessor := meta.NewAccessor()
		eventHandlers := cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				new := obj.(runtime.Object)
				addTypeInformationToObject(new)

				// Queue the namespace for further processing
				objectName, _ := metaAccessor.Name(new)
				namespaceName, err := metaAccessor.Namespace(new)
				if err != nil {
					klog.Errorf("failed accessing namespace name for %s/%s: %v", new.GetObjectKind().GroupVersionKind().Kind, objectName, err)
					return
				}

				namespace, err := namespaceLister.Get(namespaceName)
				if err != nil {
					klog.Errorf("failed loading namespace <%s> for %s/%s: %v", namespaceName, new.GetObjectKind().GroupVersionKind().Kind, objectName, err)
					return
				}

				klog.Infof("queuing namespace <%s> for processing due to update to %s/%s", namespace.Name, new.GetObjectKind().GroupVersionKind().Kind, objectName)
				controller.EnqueueNamespace(namespace)
			},
			UpdateFunc: func(newObj, oldObj interface{}) {
				new := newObj.(runtime.Object)
				old := oldObj.(runtime.Object)
				addTypeInformationToObject(new)
				addTypeInformationToObject(old)

				// Load resource versions of the new and old object.
				// If they are the same, then the objects have not changed
				// and we don't need to continue processing it. This happens
				// when the informer is re-synchronized against the API server.
				newResourceVersion, err := metaAccessor.ResourceVersion(new)
				if err != nil {
					klog.Errorf("failed loading resource version: %v", err)
					return
				}

				oldResourceVersion, err := metaAccessor.ResourceVersion(old)
				if err != nil {
					klog.Errorf("failed loading resource version: %v", err)
					return
				}

				if newResourceVersion == oldResourceVersion {
					return
				}

				// Queue the namespace for further processing
				objectName, _ := metaAccessor.Name(new)
				namespaceName, err := metaAccessor.Namespace(new)
				if err != nil {
					klog.Errorf("failed accessing namespace name for %s/%s: %v", new.GetObjectKind().GroupVersionKind().Kind, objectName, err)
					return
				}

				namespace, err := namespaceLister.Get(namespaceName)
				if err != nil {
					klog.Errorf("failed loading namespace <%s> for %s/%s: %v", namespaceName, new.GetObjectKind().GroupVersionKind().Kind, objectName, err)
					return
				}

				klog.Infof("queuing namespace <%s> for processing due to update to %s/%s", namespace.Name, new.GetObjectKind().GroupVersionKind().Kind, objectName)
				controller.EnqueueNamespace(namespace)
			},
		}

		podInformer.Informer().AddEventHandler(eventHandlers)
		pvcInformer.Informer().AddEventHandler(eventHandlers)

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
	rootCmd.AddCommand(financeCmd)
}
