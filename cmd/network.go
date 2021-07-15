package cmd

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"time"

	"github.com/StatCan/namespace-controller/pkg/controllers/namespaces"
	"github.com/StatCan/namespace-controller/pkg/signals"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
)

var networkCmd = &cobra.Command{
	Use:   "network",
	Short: "Configure network resources for namespaces.",
	Long: `Configure network resources for namespaces.
* Network policies
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

		networkPolicyInformer := kubeInformerFactory.Networking().V1().NetworkPolicies()
		networkPolicyLister := networkPolicyInformer.Lister()

		// Listen for endpoints for the `kubernetes` service
		kubeDefaultNsInformerFactory := kubeinformers.NewSharedInformerFactoryWithOptions(kubeClient, time.Minute*5, kubeinformers.WithNamespace("default"))

		defaultNsEndpointsInformer := kubeDefaultNsInformerFactory.Core().V1().Endpoints()
		defaultNsEndpointsLister := defaultNsEndpointsInformer.Lister()

		// Setup controller
		controller := namespaces.NewNamespacesController(
			kubeInformerFactory.Core().V1().Namespaces(),
			func(namespace *corev1.Namespace) error {
				// Skip 'control-plane' namespaces
				if _, ok := namespace.ObjectMeta.Labels["control-plane"]; ok {
					klog.Infof("skipping namespace <%v> as it is a cluster control plane namespace", namespace.Name)
					return nil
				}

				// Create default network policies to prevent ingress traffic
				apiServerEndpoints, err := defaultNsEndpointsLister.Endpoints("default").Get("kubernetes")
				if err != nil {
					return fmt.Errorf("failed to list endpoints of Kubernetes API server: %v", err)
				}

				policies := generateNetworkPolicies(namespace, apiServerEndpoints)

				for _, policy := range policies {
					currentPolicy, err := networkPolicyLister.NetworkPolicies(policy.Namespace).Get(policy.Name)
					if errors.IsNotFound(err) {
						klog.Infof("creating network policy %s/%s", policy.Namespace, policy.Name)
						currentPolicy, err = kubeClient.NetworkingV1().NetworkPolicies(policy.Namespace).Create(context.Background(), policy, metav1.CreateOptions{})
						if err != nil {
							return err
						}
					}

					if !reflect.DeepEqual(policy.Spec, currentPolicy.Spec) {
						klog.Infof("updating network policy %s/%s", policy.Namespace, policy.Name)
						currentPolicy.Spec = policy.Spec

						_, err = kubeClient.NetworkingV1().NetworkPolicies(policy.Namespace).Update(context.Background(), currentPolicy, metav1.UpdateOptions{})
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
		kubeDefaultNsInformerFactory.Start(stopCh)

		// Wait for caches
		klog.Info("Waiting for informer caches to sync")
		if ok := cache.WaitForCacheSync(stopCh, networkPolicyInformer.Informer().HasSynced, defaultNsEndpointsInformer.Informer().HasSynced); !ok {
			klog.Fatalf("failed to wait for caches to sync")
		}

		// Run the controller
		if err = controller.Run(2, stopCh); err != nil {
			klog.Fatal("error running controller: %v", err)
		}
	},
}

func generateNetworkPolicies(namespace *corev1.Namespace, apiServerEndpoints *corev1.Endpoints) []*networkingv1.NetworkPolicy {
	policies := []*networkingv1.NetworkPolicy{}

	// Namespace metadata
	isSystem := false
	if val, ok := namespace.ObjectMeta.Labels["namespace.statcan.gc.ca/purpose"]; ok {
		isSystem = val == "system"
	}

	// Helpers
	protocolTCP := corev1.ProtocolTCP
	protocolUDP := corev1.ProtocolUDP

	// Default deny all ingress traffic
	policies = append(policies, &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-deny",
			Namespace: namespace.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(namespace, corev1.SchemeGroupVersion.WithKind("Namespace")),
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
		},
	})

	// Optionally allow same namespace is a label is set,
	// but assume this label by default on system namespaces
	allowSameNamespace := false
	allowSameNamespaceValue, ok := namespace.ObjectMeta.Labels["network.statcan.gc.ca/allow-same-ns"]

	if isSystem {
		if !ok {
			allowSameNamespace = true
		} else {
			allow, err := strconv.ParseBool(allowSameNamespaceValue)
			if err != nil {
				klog.Warningf("invalid boolean value %q for network.statcan.gc.ca/allow-same-ns on namespace %q; ignoring", allowSameNamespaceValue, namespace.Name)
			} else {
				allowSameNamespace = allow
			}
		}
	} else {
		allow, err := strconv.ParseBool(allowSameNamespaceValue)
		if err != nil {
			klog.Warningf("invalid boolean value %q for network.statcan.gc.ca/allow-same-ns on namespace %q; ignoring", allowSameNamespaceValue, namespace.Name)
		} else {
			allowSameNamespace = allow
		}
	}

	if allowSameNamespace {
		policies = append(policies, &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "allow-same-namespace",
				Namespace: namespace.Name,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(namespace, corev1.SchemeGroupVersion.WithKind("Namespace")),
				},
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
				Ingress: []networkingv1.NetworkPolicyIngressRule{
					{
						From: []networkingv1.NetworkPolicyPeer{
							{
								PodSelector: &metav1.LabelSelector{},
							},
						},
					},
				},
				Egress: []networkingv1.NetworkPolicyEgressRule{
					{
						To: []networkingv1.NetworkPolicyPeer{
							{
								PodSelector: &metav1.LabelSelector{},
							},
						},
					},
				},
			},
		})
	}

	// Default allow ingress controller
	if val, ok := namespace.ObjectMeta.Labels["network.statcan.gc.ca/allow-ingress-controller"]; ok {
		allow, err := strconv.ParseBool(val)
		if err != nil {
			klog.Warningf("invalid boolean value %q for network.statcan.gc.ca/allow-ingress-controller on namespace %q; ignoring", val, namespace.Name)
		} else if allow {
			policies = append(policies, &networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "default-allow-ingress-controller",
					Namespace: namespace.Name,
					OwnerReferences: []metav1.OwnerReference{
						*metav1.NewControllerRef(namespace, corev1.SchemeGroupVersion.WithKind("Namespace")),
					},
				},
				Spec: networkingv1.NetworkPolicySpec{
					PodSelector: metav1.LabelSelector{},
					PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
					Ingress: []networkingv1.NetworkPolicyIngressRule{
						{
							From: []networkingv1.NetworkPolicyPeer{
								{
									NamespaceSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"install.operator.istio.io/owner-name": "istio",
											"namespace.statcan.gc.ca/purpose":      "system",
										},
									},
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"istio": "ingressgateway",
										},
									},
								},
							},
						},
					},
				},
			})
		}
	}

	// Allow access to core system components necessary for standard operation
	// (e.g., DNS)
	dnsPort := intstr.FromInt(53)

	policies = append(policies, &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-allow-core-system",
			Namespace: namespace.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(namespace, corev1.SchemeGroupVersion.WithKind("Namespace")),
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"install.operator.istio.io/owner-name": "istio",
									"namespace.statcan.gc.ca/purpose":      "system",
								},
							},
							PodSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "istio",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{"pilot"},
									},
								},
							},
						},
					},
				},
			},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/cluster-service": "true",
								},
							},
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"k8s-app": "kube-dns",
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &protocolUDP,
							Port:     &dnsPort,
						},
						{
							Protocol: &protocolTCP,
							Port:     &dnsPort,
						},
					},
				},
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"install.operator.istio.io/owner-name": "istio",
									"namespace.statcan.gc.ca/purpose":      "system",
								},
							},
							PodSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "istio",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{"pilot"},
									},
								},
							},
						},
					},
				},
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"install.operator.istio.io/owner-name": "istio",
									"namespace.statcan.gc.ca/purpose":      "system",
								},
							},
							PodSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "istio",
										Operator: metav1.LabelSelectorOpIn,
										Values:   []string{"mixer"},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	// Allow access to kube-apiserver to workloads with the necessary label
	// However, system namespaces will have this by default.
	var apiServerPolicy *networkingv1.NetworkPolicy

	if isSystem {
		apiServerPolicy = &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "allow-kube-apiserver",
				Namespace: namespace.Name,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(namespace, corev1.SchemeGroupVersion.WithKind("Namespace")),
				},
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress:      []networkingv1.NetworkPolicyEgressRule{},
			},
		}
	} else {
		apiServerPolicy = &networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "optional-allow-kube-apiserver",
				Namespace: namespace.Name,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(namespace, corev1.SchemeGroupVersion.WithKind("Namespace")),
				},
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{
					MatchLabels: map[string]string{
						"network.statcan.gc.ca/allow-kube-apiserver": "true",
					},
				},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
				Egress:      []networkingv1.NetworkPolicyEgressRule{},
			},
		}
	}

	for _, subset := range apiServerEndpoints.Subsets {
		egressRule := networkingv1.NetworkPolicyEgressRule{
			To:    []networkingv1.NetworkPolicyPeer{},
			Ports: []networkingv1.NetworkPolicyPort{},
		}

		for _, address := range subset.Addresses {
			ip := net.ParseIP(address.IP)

			if ip == nil {
				klog.Warningf("failed to parse IP: %v", address.IP)
				continue
			}

			netmask := 0
			if ip.To4() != nil {
				netmask = 32
			} else if ip.To16() != nil {
				netmask = 128
			} else {
				klog.Warningf("skipping %q: unable to detmine if IPv4 or IPv6 address", address.IP)
				continue
			}

			egressRule.To = append(egressRule.To, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{
					CIDR: fmt.Sprintf("%s/%d", ip.String(), netmask),
				},
			})
		}

		for _, port := range subset.Ports {
			portNum := intstr.FromInt(int(port.Port))
			egressRule.Ports = append(egressRule.Ports, networkingv1.NetworkPolicyPort{
				Protocol: &port.Protocol,
				Port:     &portNum,
			})
		}

		apiServerPolicy.Spec.Egress = append(apiServerPolicy.Spec.Egress, egressRule)
	}

	policies = append(policies, apiServerPolicy)

	return policies
}

func init() {
	rootCmd.AddCommand(networkCmd)
}
