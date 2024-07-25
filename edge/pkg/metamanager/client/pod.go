package client

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	"github.com/kubeedge/beehive/pkg/core/model"
	"github.com/kubeedge/kubeedge/common/constants"
	"github.com/kubeedge/kubeedge/edge/pkg/common/cache"
	"github.com/kubeedge/kubeedge/edge/pkg/common/message"
	"github.com/kubeedge/kubeedge/edge/pkg/common/modules"
	metamanagerconfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao"
)

// PodsGetter is interface to get pods
type PodsGetter interface {
	Pods(namespace string) PodsInterface
}

// PodsInterface is pod interface
type PodsInterface interface {
	Create(*corev1.Pod) (*corev1.Pod, error)
	Update(*corev1.Pod) error
	Patch(name string, patchBytes []byte) (*corev1.Pod, error)
	Delete(name string, options metav1.DeleteOptions) error
	Get(name string) (*corev1.Pod, error)
}

type pods struct {
	namespace string
	send      SendInterface
}

// PodResp represents pod response from the api-server
type PodResp struct {
	Object *corev1.Pod
	Err    apierrors.StatusError
}

func newPods(namespace string, s SendInterface) *pods {
	return &pods{
		send:      s,
		namespace: namespace,
	}
}

func (c *pods) Create(cm *corev1.Pod) (*corev1.Pod, error) {
	resource := fmt.Sprintf("%s/%s/%s", c.namespace, model.ResourceTypePod, cm.Name)
	podMsg := message.BuildMsg(modules.MetaGroup, "", modules.EdgedModuleName, resource, model.InsertOperation, *cm)
	resp, err := c.send.SendSync(podMsg)
	if err != nil {
		return nil, fmt.Errorf("create pod failed, err: %v", err)
	}

	content, err := resp.GetContentData()
	if err != nil {
		return nil, fmt.Errorf("parse message to pod failed, err: %v", err)
	}

	return handlePodResp(resource, content)
}

func (c *pods) Update(cm *corev1.Pod) error {
	return nil
}

func (c *pods) Delete(name string, options metav1.DeleteOptions) error {
	resource := fmt.Sprintf("%s/%s/%s", c.namespace, model.ResourceTypePod, name)

	// skip updating pog when pod is in migration
	if skipPod(c.namespace, name) {
		klog.Warningf("Skip deleting pod(%s) because pod is in migration", resource)
		return nil
	}

	podDeleteMsg := message.BuildMsg(modules.MetaGroup, "", modules.EdgedModuleName, resource, model.DeleteOperation, options)
	msg, err := c.send.SendSync(podDeleteMsg)
	if err != nil {
		return err
	}

	content, ok := msg.Content.(string)
	if ok && content == constants.MessageSuccessfulContent {
		return nil
	}

	err, ok = msg.Content.(error)
	if ok {
		return err
	}

	return fmt.Errorf("delete pod failed, response content type unsupported")
}

func (c *pods) Get(name string) (*corev1.Pod, error) {
	resource := fmt.Sprintf("%s/%s/%s", c.namespace, model.ResourceTypePod, name)
	podMsg := message.BuildMsg(modules.MetaGroup, "", modules.EdgedModuleName, resource, model.QueryOperation, nil)
	msg, err := c.send.SendSync(podMsg)
	if err != nil {
		return nil, fmt.Errorf("get pod failed, err: %v", err)
	}

	content, err := msg.GetContentData()
	if err != nil {
		return nil, fmt.Errorf("parse message to pod failed, err: %v", err)
	}

	return handlePodFromMetaDB(name, content)
}

func (c *pods) Patch(name string, patchBytes []byte) (*corev1.Pod, error) {
	klog.V(3).Infof("Patching message: %s", string(patchBytes))
	resource := fmt.Sprintf("%s/%s/%s", c.namespace, model.ResourceTypePodPatch, name)
	if name == constants.DeafultMosquittoContainerName {
		return handleMqttMeta()
	}

	// skip updating pog when pod is in migration
	if skipPod(c.namespace, name) {
		klog.Warningf("Skip updating pod(%s) because pod is in migration", resource)
		return &corev1.Pod{}, nil
	}
	klog.V(3).Infof("Prepare to send message")
	podMsg := message.BuildMsg(modules.MetaGroup, "", modules.EdgedModuleName, resource, model.PatchOperation, string(patchBytes))
	klog.V(3).Infof("Ready to send message")
	resp, err := c.send.SendSync(podMsg)
	klog.V(3).Infof("message sent: %v with resp: %v and err: %v", podMsg, resp, err)
	if err != nil {
		klog.V(3).Infof("failed to patch msg: %v with error: %v and response %v", podMsg, err, resp)
		return nil, fmt.Errorf("update pod failed, err: %v", err)
	}

	klog.V(3).Infof("After patching, are we here? Patching msg: %s", string(patchBytes))
	content, err := resp.GetContentData()
	if err != nil {
		return nil, fmt.Errorf("parse message to pod failed, err: %v", err)
	}
	klog.V(3).Infof("Ready to handle pod response")
	return handlePodResp(resource, content)
}

func handlePodFromMetaDB(name string, content []byte) (*corev1.Pod, error) {
	var lists []string
	err := json.Unmarshal(content, &lists)
	if err != nil {
		return nil, fmt.Errorf("unmarshal message to pod list from db failed, err: %v", err)
	}

	if len(lists) == 0 {
		return nil, apierrors.NewNotFound(schema.GroupResource{
			Group:    "",
			Resource: "pod",
		}, name)
	}

	if len(lists) != 1 {
		return nil, fmt.Errorf("pod length from meta db is %d", len(lists))
	}

	var pod *corev1.Pod
	err = json.Unmarshal([]byte(lists[0]), &pod)
	if err != nil {
		return nil, fmt.Errorf("unmarshal message to pod failed, err: %v", err)
	}
	return pod, nil
}

func handlePodResp(resource string, content []byte) (*corev1.Pod, error) {
	klog.V(3).Infof("In function handlePodResp")
	klog.V(3).Infof("The response is: %v", string(content))
	var podResp PodResp
	err := json.Unmarshal(content, &podResp)
	if err != nil {
		return nil, fmt.Errorf("unmarshal message to pod failed, err: %v", err)
	}

	klog.V(3).Infof("Comparing pod response...")
	if reflect.DeepEqual(podResp.Err, apierrors.StatusError{}) {
		klog.V(3).Infof("Response shows everything is okay")
		if err = updatePodDB(resource, podResp.Object); err != nil {
			klog.Infof("updatePodDB with err %v", err)
			return nil, fmt.Errorf("update pod meta failed, err: %v", err)
		}
		return podResp.Object, nil
	}
	klog.V(3).Infof("Patching pod status error with err: %v. Pod Resp: %#v", podResp.Err, podResp.Object)
	return podResp.Object, &podResp.Err
}

// updatePodDB update pod meta when patch pod successful
func updatePodDB(resource string, pod *corev1.Pod) error {
	klog.V(3).Infof("in func updatePodDB")
	pod.APIVersion = "v1"
	pod.Kind = "Pod"
	podContent, err := json.Marshal(pod)
	if err != nil {
		klog.Errorf("unmarshal resp pod failed, err: %v", err)
		return err
	}
	klog.V(3).Infof("Marshal OK")

	podKey := strings.Replace(resource,
		constants.ResourceSep+model.ResourceTypePodPatch+constants.ResourceSep,
		constants.ResourceSep+model.ResourceTypePod+constants.ResourceSep, 1)
	klog.V(3).Infof("Ready to update pod info: %v", podKey)
	meta := &dao.Meta{
		Key:   podKey,
		Type:  model.ResourceTypePod,
		Node:  pod.Status.HostIP,
		Value: string(podContent)}
	klog.V(3).Infof("Patching pod info succeeded, reaady to update DB. Pod info: %#v", meta)
	return dao.InsertOrUpdate(meta)
}

func handleMqttMeta() (*corev1.Pod, error) {
	var pod corev1.Pod
	metas, err := dao.QueryMeta("key", fmt.Sprintf("default/pod/%s", constants.DeafultMosquittoContainerName))
	if err != nil || len(*metas) != 1 {
		return nil, fmt.Errorf("get mqtt meta failed, err: %v", err)
	}

	err = json.Unmarshal([]byte((*metas)[0]), &pod)
	if err != nil {
		return nil, fmt.Errorf("unmarshal mqtt meta failed, err: %v", err)
	}
	return &pod, nil
}

func skipPod(namespace, name string) bool {
	// get pod info from cache
	resKey := fmt.Sprintf("%s/%s/%s/%s", metamanagerconfig.Config.NodeId, namespace, model.ResourceTypePod, name)
	res := cache.GetPodToBeMigrated(resKey)
	if res != nil {
		if res.Status == constants.PodStatusMigrated {
			return true
		}
		if res.NodeId != metamanagerconfig.Config.NodeId {
			klog.Warningf("Skip pod(%s) because pod has been migrated to node(%s)", resKey, res.NodeId)
			return true
		}
	}
	return false
}
