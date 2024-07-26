package client

import (
	"fmt"
	"github.com/kubeedge/beehive/pkg/core/model"
	"github.com/kubeedge/kubeedge/edge/pkg/common/message"
	"github.com/kubeedge/kubeedge/edge/pkg/common/modules"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	appcorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	"k8s.io/klog/v2"
)

type EventsGetter interface {
	Events(namespace string) EventsInterface
}

type EventsInterface interface {
	Create(*corev1.Event, metav1.CreateOptions) (*corev1.Event, error)
	Update(*corev1.Event, metav1.UpdateOptions) (*corev1.Event, error)
	Patch(string, types.PatchType, []byte, metav1.PatchOptions) (*corev1.Event, error)
	Delete(string, metav1.DeleteOptions) error
	Get(string, metav1.GetOptions) (*corev1.Event, error)
	Apply(*appcorev1.EventApplyConfiguration, metav1.ApplyOptions) (result *corev1.Event, err error)

	CreateWithEventNamespace(*corev1.Event) (*corev1.Event, error)
	UpdateWithEventNamespace(*corev1.Event) (*corev1.Event, error)
	PatchWithEventNamespace(*corev1.Event, []byte) (*corev1.Event, error)
}

type events struct {
	send      SendInterface
	namespace string
}

func newEvents(namespace string, s SendInterface) *events {
	return &events{
		send:      s,
		namespace: namespace,
	}
}

func (e *events) Create(event *corev1.Event, opts metav1.CreateOptions) (*corev1.Event, error) {
	klog.Infof("Create event %+v with options %+v", event, opts)
	return event, nil
}

func (e *events) Update(event *corev1.Event, opts metav1.UpdateOptions) (*corev1.Event, error) {
	klog.Infof("Update event %+v with options %+v", event, opts)
	return event, nil
}

func (e *events) Patch(name string, pt types.PatchType, data []byte, opts metav1.PatchOptions) (*corev1.Event, error) {
	klog.Infof("Patch event %s with patchtype %s, data %s and options %+v", name, pt, string(data), opts)
	return &corev1.Event{}, nil
}

func (e *events) Delete(name string, opts metav1.DeleteOptions) error {
	klog.Infof("Delete event %s with options %+v", name, opts)
	return nil
}

func (e *events) Get(name string, opts metav1.GetOptions) (*corev1.Event, error) {
	klog.Infof("Get event %s with option %+v", name, opts)
	return &corev1.Event{}, nil
}

func (e *events) Apply(event *appcorev1.EventApplyConfiguration, opts metav1.ApplyOptions) (*corev1.Event, error) {
	klog.Infof("Apply event %+v with options %+v", event, opts)
	return &corev1.Event{}, nil
}

func (e *events) CreateWithEventNamespace(event *corev1.Event) (*corev1.Event, error) {
	klog.Infof("666666: Create event with ns: %+v", event)
	resource := fmt.Sprintf("%s/%s/%s", e.namespace, model.ResourceTypeEvent, event.Name)
	eventMsg := message.BuildMsg(modules.MetaGroup, "", modules.EdgedModuleName, resource, model.InsertOperation, event)
	e.send.Send(eventMsg)
	//if err != nil {
	//	return nil, fmt.Errorf("create event failed, err: %v", err)
	//}
	//
	//_, err = resp.GetContentData()
	//if err != nil {
	//	return nil, fmt.Errorf("parse event failed, err: %v", err)
	//}
	return event, nil
}

func (e *events) UpdateWithEventNamespace(event *corev1.Event) (*corev1.Event, error) {
	klog.Infof("666666: Update event with ns: %+v", event)
	resource := fmt.Sprintf("%s/%s/%s", e.namespace, model.ResourceTypeEvent, event.Name)
	eventMsg := message.BuildMsg(modules.MetaGroup, "", modules.EdgedModuleName, resource, model.UpdateOperation, event)
	e.send.Send(eventMsg)
	//if err != nil {
	//	return nil, fmt.Errorf("update event failed, err: %v", err)
	//}
	//
	//_, err = resp.GetContentData()
	//if err != nil {
	//	return nil, fmt.Errorf("parse event failed, err: %v", err)
	//}
	return event, nil
}

func (e *events) PatchWithEventNamespace(event *corev1.Event, data []byte) (*corev1.Event, error) {
	klog.Infof("666666: Patch event with ns: %+v and data: %s", event, string(data))
	resource := fmt.Sprintf("%s/%s/%s", e.namespace, model.ResourceTypeEvent, event.Name)
	eventMsg := message.BuildMsg(modules.MetaGroup, "", modules.EdgedModuleName, resource, model.PatchOperation, string(data))
	e.send.Send(eventMsg)
	//if err != nil {
	//	return nil, fmt.Errorf("patch event failed, err: %v", err)
	//}
	//
	//_, err = resp.GetContentData()
	//if err != nil {
	//	return nil, fmt.Errorf("parse event failed, err: %v", err)
	//}
	return event, nil
}

// Todo 改3个函数，看edgehub
