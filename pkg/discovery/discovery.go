package discovery

import (
	"context"
	"fmt"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/k8s-topo-cli/pkg/client"
)

type ResourceCounts struct {
	Namespaces   int
	Deployments  int
	StatefulSets int
	DaemonSets   int
	ReplicaSets  int
	Pods         int
	Services     int
	Ingresses    int
	PVCs         int
	PVs          int
	ConfigMaps   int
	Secrets      int
	Jobs         int
	CronJobs     int
}

type DiscoveredResources struct {
	Namespaces   []*corev1.Namespace
	Deployments  []*appsv1.Deployment
	StatefulSets []*appsv1.StatefulSet
	DaemonSets   []*appsv1.DaemonSet
	ReplicaSets  []*appsv1.ReplicaSet
	Pods         []*corev1.Pod
	Services     []*corev1.Service
	Ingresses    []*networkingv1.Ingress
	PVCs         []*corev1.PersistentVolumeClaim
	PVs          []*corev1.PersistentVolume
	ConfigMaps   []*corev1.ConfigMap
	Secrets      []*corev1.Secret
	Jobs         []*batchv1.Job
	Nodes        []*corev1.Node
	Events       []*corev1.Event
	Counts       ResourceCounts
}

type Discoverer struct {
	client    *client.ClusterClient
	namespace string
	batchSize int
	maxConns  int
}

func NewDiscoverer(c *client.ClusterClient, namespace string) *Discoverer {
	return &Discoverer{
		client:    c,
		namespace: namespace,
		batchSize: 200,
		maxConns:  10,
	}
}

type discoverResult struct {
	namespaces   []*corev1.Namespace
	deployments  []*appsv1.Deployment
	statefulSets []*appsv1.StatefulSet
	daemonSets   []*appsv1.DaemonSet
	replicaSets  []*appsv1.ReplicaSet
	pods         []*corev1.Pod
	services     []*corev1.Service
	ingresses    []*networkingv1.Ingress
	pvcs         []*corev1.PersistentVolumeClaim
	pvs          []*corev1.PersistentVolume
	configMaps   []*corev1.ConfigMap
	secrets      []*corev1.Secret
	nodes        []*corev1.Node
	events       []*corev1.Event
	err          error
	name         string
}

func (d *Discoverer) Discover(ctx context.Context) (*DiscoveredResources, error) {
	resultCh := make(chan discoverResult, 14)
	sem := make(chan struct{}, d.maxConns)

	var wg sync.WaitGroup

	launch := func(name string, fn func() discoverResult) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := fn()
			r.name = name
			resultCh <- r
		}()
	}

	launch("namespaces", func() discoverResult {
		list, err := d.client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.Namespace, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{namespaces: items}
	})

	launch("deployments", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*appsv1.Deployment, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{deployments: items}
	})

	launch("statefulsets", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*appsv1.StatefulSet, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{statefulSets: items}
	})

	launch("daemonsets", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*appsv1.DaemonSet, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{daemonSets: items}
	})

	launch("replicasets", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*appsv1.ReplicaSet, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{replicaSets: items}
	})

	launch("pods", func() discoverResult {
		var items []*corev1.Pod
		ns := d.namespace
		if ns == "" {
			continueToken := ""
			for {
				list, err := d.client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
					Limit:    int64(d.batchSize),
					Continue: continueToken,
				})
				if err != nil {
					return discoverResult{err: err}
				}
				for i := range list.Items {
					items = append(items, &list.Items[i])
				}
				if list.Continue == "" {
					break
				}
				continueToken = list.Continue
			}
		} else {
			list, err := d.client.Clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return discoverResult{err: err}
			}
			items = make([]*corev1.Pod, len(list.Items))
			for i := range list.Items {
				items[i] = &list.Items[i]
			}
		}
		return discoverResult{pods: items}
	})

	launch("services", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.Service, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{services: items}
	})

	launch("ingresses", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*networkingv1.Ingress, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{ingresses: items}
	})

	launch("pvcs", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.PersistentVolumeClaim, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{pvcs: items}
	})

	launch("pvs", func() discoverResult {
		list, err := d.client.Clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.PersistentVolume, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{pvs: items}
	})

	launch("configmaps", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.ConfigMap, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{configMaps: items}
	})

	launch("secrets", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.Secret, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{secrets: items}
	})

	launch("nodes", func() discoverResult {
		list, err := d.client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.Node, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{nodes: items}
	})

	launch("events", func() discoverResult {
		ns := d.namespace
		list, err := d.client.Clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return discoverResult{err: err}
		}
		items := make([]*corev1.Event, len(list.Items))
		for i := range list.Items {
			items[i] = &list.Items[i]
		}
		return discoverResult{events: items}
	})

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	resources := &DiscoveredResources{}
	var errs []error

	for r := range resultCh {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))
			continue
		}
		if r.namespaces != nil {
			resources.Namespaces = append(resources.Namespaces, r.namespaces...)
		}
		if r.deployments != nil {
			resources.Deployments = append(resources.Deployments, r.deployments...)
		}
		if r.statefulSets != nil {
			resources.StatefulSets = append(resources.StatefulSets, r.statefulSets...)
		}
		if r.daemonSets != nil {
			resources.DaemonSets = append(resources.DaemonSets, r.daemonSets...)
		}
		if r.replicaSets != nil {
			resources.ReplicaSets = append(resources.ReplicaSets, r.replicaSets...)
		}
		if r.pods != nil {
			resources.Pods = append(resources.Pods, r.pods...)
		}
		if r.services != nil {
			resources.Services = append(resources.Services, r.services...)
		}
		if r.ingresses != nil {
			resources.Ingresses = append(resources.Ingresses, r.ingresses...)
		}
		if r.pvcs != nil {
			resources.PVCs = append(resources.PVCs, r.pvcs...)
		}
		if r.pvs != nil {
			resources.PVs = append(resources.PVs, r.pvs...)
		}
		if r.configMaps != nil {
			resources.ConfigMaps = append(resources.ConfigMaps, r.configMaps...)
		}
		if r.secrets != nil {
			resources.Secrets = append(resources.Secrets, r.secrets...)
		}
		if r.nodes != nil {
			resources.Nodes = append(resources.Nodes, r.nodes...)
		}
		if r.events != nil {
			resources.Events = append(resources.Events, r.events...)
		}
	}

	resources.Counts = ResourceCounts{
		Namespaces:   len(resources.Namespaces),
		Deployments:  len(resources.Deployments),
		StatefulSets: len(resources.StatefulSets),
		DaemonSets:   len(resources.DaemonSets),
		ReplicaSets:  len(resources.ReplicaSets),
		Pods:         len(resources.Pods),
		Services:     len(resources.Services),
		Ingresses:    len(resources.Ingresses),
		PVCs:         len(resources.PVCs),
		PVs:          len(resources.PVs),
		ConfigMaps:   len(resources.ConfigMaps),
		Secrets:      len(resources.Secrets),
		Jobs:         len(resources.Jobs),
	}

	if len(errs) > 0 {
		return resources, fmt.Errorf("discovery completed with %d errors: %v", len(errs), errs)
	}

	return resources, nil
}

func GetPodsByOwner(pods []*corev1.Pod, ownerUID types.UID) []*corev1.Pod {
	var result []*corev1.Pod
	for _, pod := range pods {
		for _, ref := range pod.OwnerReferences {
			if ref.UID == ownerUID {
				result = append(result, pod)
			}
		}
	}
	return result
}

func GetPodsBySelector(pods []*corev1.Pod, selector labels.Selector) []*corev1.Pod {
	var result []*corev1.Pod
	for _, pod := range pods {
		if selector.Matches(labels.Set(pod.Labels)) {
			result = append(result, pod)
		}
	}
	return result
}

func GetServicePods(svc *corev1.Service, pods []*corev1.Pod) []*corev1.Pod {
	if len(svc.Spec.Selector) == 0 {
		return nil
	}
	selector := labels.Set(svc.Spec.Selector).AsSelectorPreValidated()
	return GetPodsBySelector(pods, selector)
}

func GetIngressServices(ing *networkingv1.Ingress, services []*corev1.Service) []*corev1.Service {
	var result []*corev1.Service
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, path := range rule.HTTP.Paths {
			svcName := path.Backend.Service.Name
			for _, svc := range services {
				if svc.Name == svcName && svc.Namespace == ing.Namespace {
					result = append(result, svc)
				}
			}
		}
	}
	if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
		svcName := ing.Spec.DefaultBackend.Service.Name
		for _, svc := range services {
			if svc.Name == svcName && svc.Namespace == ing.Namespace {
				result = append(result, svc)
			}
		}
	}
	return result
}

func GetPVForPVC(pvc *corev1.PersistentVolumeClaim, pvs []*corev1.PersistentVolume) *corev1.PersistentVolume {
	if pvc.Spec.VolumeName == "" {
		return nil
	}
	for _, pv := range pvs {
		if pv.Name == pvc.Spec.VolumeName {
			return pv
		}
	}
	return nil
}
