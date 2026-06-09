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
	Namespaces      int
	Deployments     int
	StatefulSets    int
	DaemonSets      int
	ReplicaSets     int
	Pods            int
	Services        int
	Ingresses       int
	PVCs            int
	PVs             int
	ConfigMaps      int
	Secrets         int
	Jobs            int
	CronJobs        int
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

func (d *Discoverer) Discover(ctx context.Context) (*DiscoveredResources, error) {
	resources := &DiscoveredResources{}
	var errs []error

	type result struct {
		name string
		err  error
	}

	ch := make(chan result, 14)
	sem := make(chan struct{}, d.maxConns)

	var wg sync.WaitGroup

	discoverFn := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := fn(); err != nil {
				ch <- result{name: name, err: err}
				return
			}
			ch <- result{name: name, err: nil}
		}()
	}

	discoverFn("namespaces", func() error {
		list, err := d.client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for i := range list.Items {
			resources.Namespaces = append(resources.Namespaces, &list.Items[i])
		}
		return nil
	})

	discoverFn("deployments", func() error {
		return d.discoverDeployments(ctx, resources)
	})

	discoverFn("statefulsets", func() error {
		return d.discoverStatefulSets(ctx, resources)
	})

	discoverFn("daemonsets", func() error {
		return d.discoverDaemonSets(ctx, resources)
	})

	discoverFn("replicasets", func() error {
		return d.discoverReplicaSets(ctx, resources)
	})

	discoverFn("pods", func() error {
		return d.discoverPods(ctx, resources)
	})

	discoverFn("services", func() error {
		return d.discoverServices(ctx, resources)
	})

	discoverFn("ingresses", func() error {
		return d.discoverIngresses(ctx, resources)
	})

	discoverFn("pvcs", func() error {
		return d.discoverPVCs(ctx, resources)
	})

	discoverFn("pvs", func() error {
		return d.discoverPVs(ctx, resources)
	})

	discoverFn("configmaps", func() error {
		return d.discoverConfigMaps(ctx, resources)
	})

	discoverFn("secrets", func() error {
		return d.discoverSecrets(ctx, resources)
	})

	discoverFn("nodes", func() error {
		return d.discoverNodes(ctx, resources)
	})

	discoverFn("events", func() error {
		return d.discoverEvents(ctx, resources)
	})

	go func() {
		wg.Wait()
		close(ch)
	}()

	for r := range ch {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))
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

func (d *Discoverer) listNamespaces(ctx context.Context) ([]string, error) {
	if d.namespace != "" {
		return []string{d.namespace}, nil
	}

	list, err := d.client.Clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	ns := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		ns = append(ns, item.Name)
	}
	return ns, nil
}

func (d *Discoverer) discoverDeployments(ctx context.Context, res *DiscoveredResources) error {
	if d.namespace != "" {
		list, err := d.client.Clientset.AppsV1().Deployments(d.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return err
		}
		for i := range list.Items {
			res.Deployments = append(res.Deployments, &list.Items[i])
		}
		return nil
	}

	list, err := d.client.Clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.Deployments = append(res.Deployments, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverStatefulSets(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	if ns == "" {
		ns = ""
	}
	list, err := d.client.Clientset.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.StatefulSets = append(res.StatefulSets, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverDaemonSets(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.DaemonSets = append(res.DaemonSets, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverReplicaSets(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.ReplicaSets = append(res.ReplicaSets, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverPods(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	if ns == "" {
		return d.discoverPodsBatch(ctx, res)
	}

	list, err := d.client.Clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.Pods = append(res.Pods, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverPodsBatch(ctx context.Context, res *DiscoveredResources) error {
	continueToken := ""
	for {
		list, err := d.client.Clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
			Limit:    int64(d.batchSize),
			Continue: continueToken,
		})
		if err != nil {
			return err
		}
		for i := range list.Items {
			res.Pods = append(res.Pods, &list.Items[i])
		}
		if list.Continue == "" {
			break
		}
		continueToken = list.Continue
	}
	return nil
}

func (d *Discoverer) discoverServices(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.Services = append(res.Services, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverIngresses(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.NetworkingV1().Ingresses(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.Ingresses = append(res.Ingresses, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverPVCs(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.PVCs = append(res.PVCs, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverPVs(ctx context.Context, res *DiscoveredResources) error {
	list, err := d.client.Clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.PVs = append(res.PVs, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverConfigMaps(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.ConfigMaps = append(res.ConfigMaps, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverSecrets(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.Secrets = append(res.Secrets, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverNodes(ctx context.Context, res *DiscoveredResources) error {
	list, err := d.client.Clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.Nodes = append(res.Nodes, &list.Items[i])
	}
	return nil
}

func (d *Discoverer) discoverEvents(ctx context.Context, res *DiscoveredResources) error {
	ns := d.namespace
	list, err := d.client.Clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		res.Events = append(res.Events, &list.Items[i])
	}
	return nil
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
