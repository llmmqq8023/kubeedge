package v1

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

// EventBridge is a structure that handles event operations.
type EventBridge struct {
	ns         string
	MetaClient client.CoreInterface
}

// Create logs the event creation request.
func (c *EventBridge) Create(ctx context.Context, event *corev1.Event, opts metav1.CreateOptions) (*corev1.Event, error) {
	klog.Infof("Creating event: %s/%s", c.ns, event.Name)
	return event, nil
}

// Update logs the event update request.
func (c *EventBridge) Update(ctx context.Context, event *corev1.Event, opts metav1.UpdateOptions) (*corev1.Event, error) {
	klog.Infof("Updating event: %s/%s", c.ns, event.Name)
	return event, nil
}

// Delete logs the event deletion request.
func (c *EventBridge) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	klog.Infof("Deleting event: %s/%s", c.ns, name)
	return nil
}

// Get logs the event retrieval request.
func (c *EventBridge) Get(ctx context.Context, name string, opts metav1.GetOptions) (*corev1.Event, error) {
	klog.Infof("Getting event: %s/%s", c.ns, name)
	return nil, nil
}
