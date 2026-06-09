package audit

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func EmitViolationEvents(ctx context.Context, clientset *kubernetes.Clientset, nsResults []NamespaceAuditResult) error {
	for _, nr := range nsResults {
		for _, v := range nr.Violations {
			if v.Action != "block" {
				continue
			}
			message := fmt.Sprintf("%s: current=%s, limit=%s", v.Dimension, v.CurrentValue, v.PolicyLimit)
			if err := createOrSkipEvent(ctx, clientset, nr.Namespace, v.Dimension, message); err != nil {
				return fmt.Errorf("failed to emit event for namespace %s: %w", nr.Namespace, err)
			}
		}
	}
	return nil
}

func createOrSkipEvent(ctx context.Context, clientset *kubernetes.Clientset, namespace string, dimension ViolationDimension, message string) error {
	ns, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get namespace %s: %w", namespace, err)
	}

	cutoff := time.Now().Add(-10 * time.Minute)
	events, err := clientset.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("reason=QuotaViolation"),
	})
	if err != nil {
		return fmt.Errorf("failed to list events in namespace %s: %w", namespace, err)
	}

	for _, e := range events.Items {
		if e.Reason == "QuotaViolation" && e.Message == message && e.LastTimestamp.After(cutoff) {
			return nil
		}
	}

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "quota-violation-",
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Namespace",
			Name:       namespace,
			UID:        ns.UID,
			Namespace:  namespace,
		},
		Reason:  "QuotaViolation",
		Message: message,
		Source: corev1.EventSource{
			Component: "k8s-topo-cli",
		},
		FirstTimestamp: metav1.Now(),
		LastTimestamp:  metav1.Now(),
		Count:         1,
		Type:          "Warning",
	}

	_, err = clientset.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create event: %w", err)
	}

	return nil
}
