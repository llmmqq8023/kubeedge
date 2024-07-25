package metamanager

import (
	"encoding/json"
	"fmt"
	"github.com/kubeedge/kubeedge/edge/pkg/common/dbm"
	"strings"
	"time"

	beehiveContext "github.com/kubeedge/beehive/pkg/core/context"
	"github.com/kubeedge/beehive/pkg/core/model"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"

	cloudmodules "github.com/kubeedge/kubeedge/cloud/pkg/common/modules"
	"github.com/kubeedge/kubeedge/common/constants"
	edgeapi "github.com/kubeedge/kubeedge/common/types"
	"github.com/kubeedge/kubeedge/edge/pkg/common/cache"
	connect "github.com/kubeedge/kubeedge/edge/pkg/common/cloudconnection"
	"github.com/kubeedge/kubeedge/edge/pkg/common/contexts"
	"github.com/kubeedge/kubeedge/edge/pkg/common/modules"
	"github.com/kubeedge/kubeedge/edge/pkg/common/util"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/client"
	metamanagerclient "github.com/kubeedge/kubeedge/edge/pkg/metamanager/client"
	metaManagerConfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/config"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/dao"
	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver/kubernetes/storage/sqlite/imitator"
	migrationserverconfig "github.com/kubeedge/kubeedge/edge/pkg/migration/config"
	migrationdao "github.com/kubeedge/kubeedge/edge/pkg/migration/dao"
)

// Constants to check metamanager processes
const (
	OK                   = "OK"
	GroupResource        = "resource"
	CloudControllerModel = "edgecontroller"
	errNotConnected      = "not connected"
)

func feedbackError(err error, request model.Message) {
	errResponse := model.NewErrorMessage(&request, err.Error()).SetRoute(modules.MetaManagerModuleName, request.GetGroup())
	if request.GetSource() == modules.EdgedModuleName {
		sendToEdged(errResponse, request.IsSync())
	} else {
		sendToCloud(errResponse)
	}
}

func feedbackResponse(message *model.Message, parentID string, resp *model.Message) {
	resp.BuildHeader(resp.GetID(), parentID, resp.GetTimestamp())
	sendToEdged(resp, message.IsSync())
	respToCloud := message.NewRespByMessage(resp, OK)
	sendToCloud(respToCloud)
}

func sendToEdged(message *model.Message, sync bool) {
	if sync {
		beehiveContext.SendResp(*message)
	} else {
		beehiveContext.Send(modules.EdgedModuleName, *message)
	}
}

func sendToMigration(message *model.Message) {
	beehiveContext.Send(modules.MigrationModuleName, *message)
}

func sendToCloud(message *model.Message) {
	beehiveContext.SendToGroup(string(metaManagerConfig.Config.ContextSendGroup), *message)
}

// Resource format: <namespace>/<restype>[/resid]
// return <reskey, restype, resid>
func parseResource(message *model.Message) (string, string, string) {
	resource := message.GetResource()
	tokens := strings.Split(resource, constants.ResourceSep)
	resType := ""
	resID := ""
	switch len(tokens) {
	case 2:
		resType = tokens[len(tokens)-1]
	case 3:
		resType = tokens[len(tokens)-2]
		resID = tokens[len(tokens)-1]
	default:
	}
	if resType != model.ResourceTypeServiceAccountToken {
		return resource, resType, resID
	}
	var tokenReq authenticationv1.TokenRequest
	content, err := message.GetContentData()
	if err != nil {
		klog.Errorf("failed to get token request from message %s, error %s", message.GetID(), err)
		return "", "", ""
	}
	if err = json.Unmarshal(content, &tokenReq); err != nil {
		klog.Errorf("failed to unmarshal token request from message %s, error %s", message.GetID(), err)
		return "", "", ""
	}

	trTokens := strings.Split(resource, constants.ResourceSep)
	if len(trTokens) != 3 {
		klog.Errorf("failed to get resource %s name and namespace", resource)
		return "", "", ""
	}
	return client.KeyFunc(trTokens[2], trTokens[0], &tokenReq), resType, ""
}

// is resource type require remote query
func requireRemoteQuery(resType string) bool {
	return resType == model.ResourceTypeConfigmap ||
		resType == model.ResourceTypeSecret ||
		resType == constants.ResourceTypePersistentVolume ||
		resType == constants.ResourceTypePersistentVolumeClaim ||
		resType == constants.ResourceTypeVolumeAttachment ||
		resType == model.ResourceTypeNode ||
		resType == model.ResourceTypeServiceAccountToken ||
		resType == model.ResourceTypeLease
}

func msgDebugInfo(message *model.Message) string {
	return fmt.Sprintf("msgID[%s] resource[%s]", message.GetID(), message.GetResource())
}

func (m *metaManager) handleMessage(message *model.Message) error {
	resKey, resType, _ := parseResource(message)
	switch message.GetOperation() {
	case model.InsertOperation, model.UpdateOperation, model.PatchOperation, model.ResponseOperation:
		content, err := message.GetContentData()
		if err != nil {
			klog.Errorf("get message content data failed, message: %s, error: %s", msgDebugInfo(message), err)
			return fmt.Errorf("get message content data failed, error: %s", err)
		}
		meta := &dao.Meta{
			Key:   resKey,
			Type:  resType,
			Node:  metaManagerConfig.Config.NodeId,
			Value: string(content)}
		klog.V(3).Infof("Updating meta msg: %#v", meta)
		err = dao.InsertOrUpdate(meta)
		if err != nil {
			klog.Errorf("insert or update meta failed, message: %s, error: %v", msgDebugInfo(message), err)
			return fmt.Errorf("insert or update meta failed, %s", err)
		}
	case model.DeleteOperation:
		if resType == model.ResourceTypePod {
			err := processDeletePodDB(*message)
			if err != nil {
				klog.Errorf("delete pod meta failed, message %s, err: %v", msgDebugInfo(message), err)
				return fmt.Errorf("failed to delete pod meta to DB: %s", err)
			}
		} else {
			err := dao.DeleteMetaByKey(resKey)
			if err != nil {
				klog.Errorf("delete meta failed, %s", msgDebugInfo(message))
				return fmt.Errorf("delete meta failed, %s", err)
			}
		}
	}
	return nil
}

func processDeletePodDB(message model.Message) error {
	var msgPod corev1.Pod
	msgContent, err := message.GetContentData()
	if err != nil {
		return err
	}

	err = json.Unmarshal(msgContent, &msgPod)
	if err != nil {
		return err
	}

	num, err := dao.DeleteMetaByKeyAndPodUID(message.GetResource(), string(msgPod.UID))
	if err != nil {
		return err
	}
	if num == 0 {
		klog.V(2).Infof("don't need to delete pod DB")
		return nil
	}

	podPatchKey := strings.Replace(message.GetResource(),
		constants.ResourceSep+model.ResourceTypePod+constants.ResourceSep,
		constants.ResourceSep+model.ResourceTypePodPatch+constants.ResourceSep, 1)
	err = dao.DeleteMetaByKey(podPatchKey)
	if err != nil {
		return err
	}

	return nil
}

func (m *metaManager) processInsert(message model.Message) {
	imitator.DefaultV2Client.Inject(message)

	msgSource := message.GetSource()
	if msgSource == modules.EdgedModuleName {
		if !connect.IsConnected() {
			klog.Warningf("process remote failed, req[%s], err: %v", msgDebugInfo(&message), errNotConnected)
			feedbackError(fmt.Errorf("failed to process remote: %s", errNotConnected), message)
			return
		}
		m.processRemote(message)
		return
	}
	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	if msgSource == cloudmodules.DeviceControllerModuleName {
		message.SetRoute(modules.MetaGroup, modules.DeviceTwinModuleName)
		beehiveContext.Send(modules.DeviceTwinModuleName, message)
	} else if msgSource != cloudmodules.PolicyControllerModuleName {
		// Notify edged
		sendToEdged(&message, false)
	}

	resp := message.NewRespByMessage(&message, OK)
	sendToCloud(resp)
}

func (m *metaManager) processUpdate(message model.Message) {
	// confirms whether to skip the step of updating pod
	toNodeId, toNodeIp, addToCache, skip := skipPod(message)
	if skip {
		if addToCache {
			addPodToCache(toNodeId, toNodeIp, constants.PodStatusMigrated, message)
		}
		resp := message.NewRespByMessage(&message, OK)
		sendToCloud(resp)
		return
	}
	// add into cache for every pod
	if addToCache && message.GetType() == model.ResourceTypePod {
		addPodToCache(metaManagerConfig.Config.NodeId, "", constants.PodStatusInitialized, message)
	}
	imitator.DefaultV2Client.Inject(message)

	msgSource := message.GetSource()
	_, resType, _ := parseResource(&message)
	if msgSource == modules.EdgedModuleName && resType == model.ResourceTypeLease {
		if !connect.IsConnected() {
			klog.Warningf("process remote failed, req[%s], err: %v", msgDebugInfo(&message), errNotConnected)
			feedbackError(fmt.Errorf("failed to process remote: %s", errNotConnected), message)
			return
		}
		m.processRemote(message)
		return
	}
	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	switch msgSource {
	case modules.EdgedModuleName:
		// For pod status update message, we need to wait for the response message
		// to ensure that the pod status is correctly reported to the kube-apiserver
		sendToCloud(&message)
		resp := message.NewRespByMessage(&message, OK)
		sendToEdged(resp, message.IsSync())
	case cloudmodules.EdgeControllerModuleName, cloudmodules.DynamicControllerModuleName:
		sendToEdged(&message, message.IsSync())
		resp := message.NewRespByMessage(&message, OK)
		sendToCloud(resp)
	case cloudmodules.DeviceControllerModuleName:
		resp := message.NewRespByMessage(&message, OK)
		sendToCloud(resp)
		message.SetRoute(modules.MetaGroup, modules.DeviceTwinModuleName)
		beehiveContext.Send(modules.DeviceTwinModuleName, message)
	case cloudmodules.PolicyControllerModuleName:
		resp := message.NewRespByMessage(&message, OK)
		sendToCloud(resp)
	case modules.MigrationModuleName:
		sendToEdged(&message, false)
	default:
		klog.Errorf("unsupport message source, %s", msgSource)
	}
}

func (m *metaManager) processPatch(message model.Message) {
	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	if !connect.IsConnected() {
		mergedPodData, err := mergePodData(message)
		if err != nil {
			feedbackError(err, message)
			return
		}
		respToEdged := message.NewRespByMessage(&message, metamanagerclient.PodResp{Object: &mergedPodData, Err: apierrors.StatusError{}})
		beehiveContext.SendResp(*respToEdged)
		return
	}
	sendToCloud(&message)
}

func (m *metaManager) processResponse(message model.Message) {
	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	// Notify edged if the data is coming from cloud
	if message.GetSource() == CloudControllerModel {
		sendToEdged(&message, message.IsSync())
	} else {
		// Send to cloud if the update request is coming from edged
		sendToCloud(&message)
	}
}

func (m *metaManager) processDelete(message model.Message) {
	imitator.DefaultV2Client.Inject(message)
	_, resType, _ := parseResource(&message)
	if resType == model.ResourceTypePod && message.GetSource() == modules.EdgedModuleName {
		// if pod is deleted in K8s, then a new delete message will be sent to edge
		sendToCloud(&message)
		return
	}

	if err := m.handleMessage(&message); err != nil {
		feedbackError(err, message)
		return
	}
	msgSource := message.GetSource()
	if msgSource == cloudmodules.DeviceControllerModuleName {
		message.SetRoute(modules.MetaGroup, modules.DeviceTwinModuleName)
		beehiveContext.Send(modules.DeviceTwinModuleName, message)
	}

	if msgSource != cloudmodules.PolicyControllerModuleName {
		// Notify edged
		sendToEdged(&message, false)
	}

	if msgSource != modules.MigrationModuleName {
		// Notify migration
		sendToMigration(&message)
	}
	resp := message.NewRespByMessage(&message, OK)
	sendToCloud(resp)
}

func (m *metaManager) processQuery(message model.Message) {
	resKey, resType, resID := parseResource(&message)
	var metas *[]string
	var err error
	if requireRemoteQuery(resType) && connect.IsConnected() {
		m.processRemote(message)
		return
	}

	if resID == "" {
		// Get specific type resources
		metas, err = dao.QueryMeta("type", resType)
	} else {
		metas, err = dao.QueryMeta("key", resKey)
	}
	if err != nil {
		klog.Errorf("query meta failed, %s", msgDebugInfo(&message))
		feedbackError(fmt.Errorf("failed to query meta in DB: %s", err), message)
	} else {
		resp := message.NewRespByMessage(&message, *metas)
		resp.SetRoute(modules.MetaManagerModuleName, resp.GetGroup())
		sendToEdged(resp, message.IsSync())
	}
}

func (m *metaManager) processRemote(message model.Message) {
	go func() {
		// TODO: retry
		originalID := message.GetID()
		message.UpdateID()
		resp, err := beehiveContext.SendSync(
			string(metaManagerConfig.Config.ContextSendModule),
			message,
			time.Duration(metaManagerConfig.Config.RemoteQueryTimeout)*time.Second)
		if err != nil {
			klog.Errorf("process remote failed, req[%s], err: %v", msgDebugInfo(&message), err)
			feedbackError(fmt.Errorf("failed to process remote: %s", err), message)
			return
		}
		klog.V(4).Infof("process remote: req[%s], resp[%s]", msgDebugInfo(&message), msgDebugInfo(&resp))
		content, ok := resp.GetContent().(string)
		if ok && content == constants.MessageSuccessfulContent {
			klog.V(4).Infof("process remote successfully")
			feedbackResponse(&message, originalID, &resp)
			return
		}
		errContent, ok := resp.GetContent().(error)
		if ok {
			klog.V(4).Infof("process remote err: %v", errContent)
			feedbackResponse(&message, originalID, &resp)
			return
		}
		mapContent, ok := resp.GetContent().(map[string]interface{})
		respDB := resp
		if ok && isObjectResp(mapContent) {
			if mapContent["Err"] != nil {
				klog.V(4).Infof("process remote objResp err: %v", mapContent["Err"])
				feedbackResponse(&message, originalID, &resp)
				return
			}
			klog.V(4).Infof("process remote objResp: %+v", mapContent["Object"])
			respDB.Content = mapContent["Object"]
		}
		if err := m.handleMessage(&respDB); err != nil {
			feedbackError(err, message)
			return
		}
		feedbackResponse(&message, originalID, &resp)
	}()
}

func (m *metaManager) processPatchMigrationInfo(message model.Message) {
	klog.V(4).Infof("process patchMigrationInfo message: %+v", message)
	if !connect.IsConnected() {
		klog.Warningf("process patch migration info failed, req[%s], err: %v", msgDebugInfo(&message), errNotConnected)
		feedbackError(fmt.Errorf("failed to process patch migration info: %s", errNotConnected), message)
		return
	}
	sendToCloud(&message)
}

func isObjectResp(data map[string]interface{}) bool {
	_, ok := data["Object"]
	if !ok {
		return false
	}
	_, ok = data["Err"]
	return ok
}

func (m *metaManager) processVolume(message model.Message) {
	klog.Info("process volume started")
	back, err := beehiveContext.SendSync(modules.EdgedModuleName, message, constants.CSISyncMsgRespTimeout)
	klog.Infof("process volume get: req[%+v], back[%+v], err[%+v]", message, back, err)
	if err != nil {
		klog.Errorf("process volume send to edged failed: %v", err)
	}

	resp := message.NewRespByMessage(&message, back.GetContent())
	sendToCloud(resp)
	klog.Infof("process volume send to cloud resp[%+v]", resp)
}

func (m *metaManager) process(message model.Message) {
	operation := message.GetOperation()

	switch operation {
	case model.InsertOperation:
		m.processInsert(message)
	case model.UpdateOperation:
		m.processUpdate(message)
	case model.PatchOperation:
		m.processPatch(message)
	case model.DeleteOperation:
		m.processDelete(message)
	case model.QueryOperation:
		m.processQuery(message)
	case model.ResponseOperation:
		m.processResponse(message)
	case constants.CSIOperationTypeCreateVolume,
		constants.CSIOperationTypeDeleteVolume,
		constants.CSIOperationTypeControllerPublishVolume,
		constants.CSIOperationTypeControllerUnpublishVolume:
		m.processVolume(message)
	case constants.MigrationInfoOperationTypeUpdate:
		m.processPatchMigrationInfo(message)
	default:
		klog.Errorf("metamanager not supported operation: %v", operation)
	}
}

func (m *metaManager) runMetaManager() {
	go func() {
		for {
			select {
			case <-beehiveContext.Done():
				klog.Warning("MetaManager main loop stop")
				return
			default:
			}
			msg, err := beehiveContext.Receive(m.Name())
			if err != nil {
				klog.Errorf("get a message %+v: %v", msg, err)
				continue
			}
			klog.V(2).Infof("[metamanager loop] get a message %+v", msg)
			m.process(msg)
		}
	}()
}

func skipPod(msg model.Message) (toNodeId, toNodeIp string, addToCache, skip bool) {
	if !migrationserverconfig.Config.Enable {
		return "", "", false, false
	}
	_, resType, _ := parseResource(&msg)
	if resType == model.ResourceTypePod {
		resKey := fmt.Sprintf("%s/%s", metaManagerConfig.Config.NodeId, msg.GetResource())
		// get pod info from cache
		res := cache.GetPodToBeMigrated(resKey)
		if res != nil {
			if res.Status == constants.PodStatusMigrated {
				klog.Warningf("Skip pod(%s) on edge node because pod is in migrated state", resKey)
				return "", "", false, true
			}
			if res.NodeId != metaManagerConfig.Config.NodeId {
				klog.Warningf("Skip pod(%s) because pod has been migrated to node(%s)", resKey, res.NodeId)
				return "", "", false, true
			}
			klog.V(4).Infof("Pod in cache: %+v", res)
			return "", "", false, false
		} else {
			// resource := fmt.Sprintf("%s/%s/%s", data.Namespace, model.ResourceTypePod, data.Name)
			// Thus nodename should be patched to it
			migratedPodData, err := migrationdao.GetMigratedPodByName(dbm.NodeName + "/" + msg.GetResource())
			if err != nil {
				klog.Errorf("Failed to get migrated pod data from DB: %v", err)
				return "", "", true, false
			}
			if migratedPodData != nil && migratedPodData.NodeId != metaManagerConfig.Config.NodeId {
				klog.Warningf("Pod(%s) is being migrated to node(%s)", resKey, migratedPodData.NodeId)
				return migratedPodData.NodeId, migratedPodData.NodeIp, true, true
			}
			klog.V(4).Infof("Migrated pod: %+v", migratedPodData)
			newPod, err := dao.QueryMetasByNodeId("key", msg.GetResource(), "")
			if err != nil {
				klog.Errorf("Failed to get pod data from DB: %v", err)
				return "", "", true, false
			}
			if nodeId, isMigrated := podIsMigrated(newPod); isMigrated {
				klog.Warningf("Skip pod(%s) because pod has been migrated to node(%s)", resKey, nodeId)
				return nodeId, "", true, true
			}
			klog.V(4).Infof("New migrated pod: %+v", newPod)
		}
		return "", "", true, false
	}
	klog.V(4).Infof("Skip checking resource(%s) with type(%s)", msg.GetResource(), resType)
	return "", "", false, false
}

func addPodToCache(nodeId, nodeIp, status string, message model.Message) {
	namespace, resType, name, err := util.ParseResourceEdge(message.GetResource(), message.GetOperation())
	if err != nil {
		klog.Errorf("Failed to parse the pod resource: %v", err)
		return
	}
	data := &contexts.Resource{
		Name:      name,
		Namespace: namespace,
		ResType:   resType,
		NodeId:    nodeId,
		NodeIp:    nodeIp,
		Status:    status,
	}
	resKey := fmt.Sprintf("%s/%s", metaManagerConfig.Config.NodeId, message.GetResource())
	klog.V(4).Infof("Adding pod(%s) to cache: %+v", resKey, *data)
	cache.AddPodToBeMigrated(resKey, data)
}

func podIsMigrated(pods *[]dao.Meta) (string, bool) {
	if pods == nil {
		return "", false
	}
	for _, pod := range *pods {
		if pod.Node != metaManagerConfig.Config.NodeId {
			return pod.Node, true
		}
	}
	return "", false
}

func mergePodData(message model.Message) (corev1.Pod, error) {
	// unmarshal patch status
	var patchStatus edgeapi.PodStatusRequest
	status, err := message.GetContentData()
	if err != nil {
		return corev1.Pod{}, err
	}
	err = json.Unmarshal(status, &patchStatus)
	if err != nil {
		return corev1.Pod{}, err
	}
	// get pod data from db
	podKey := strings.Replace(message.GetResource(),
		constants.ResourceSep+model.ResourceTypePodPatch+constants.ResourceSep,
		constants.ResourceSep+model.ResourceTypePod+constants.ResourceSep, 1)
	metas, err := dao.QueryMeta("key", podKey)
	if err != nil {
		return corev1.Pod{}, err
	}
	if metas == nil || len(*metas) == 0 {
		return corev1.Pod{}, fmt.Errorf("no pod found in db")
	}
	// unmarshal pod data
	var msgPod corev1.Pod
	err = json.Unmarshal([]byte((*metas)[0]), &msgPod)
	if err != nil {
		return corev1.Pod{}, err
	}
	// merge pod status
	msgPod.Status = mergePodStatus(msgPod.Status, patchStatus.Status)

	return msgPod, nil
}

func mergePodStatus(originalStatus, patchStatus corev1.PodStatus) corev1.PodStatus {
	status := originalStatus.DeepCopy()
	if len(patchStatus.Phase) != 0 {
		status.Phase = patchStatus.Phase
	}
	if len(patchStatus.Message) != 0 {
		status.Message = patchStatus.Message
	}
	if len(patchStatus.Reason) != 0 {
		status.Reason = patchStatus.Reason
	}
	if len(patchStatus.NominatedNodeName) != 0 {
		status.NominatedNodeName = patchStatus.NominatedNodeName
	}
	if len(patchStatus.HostIP) != 0 {
		status.HostIP = patchStatus.HostIP
	}
	if len(patchStatus.PodIP) != 0 && len(patchStatus.PodIPs) != 0 {
		status.PodIP = patchStatus.PodIP
		status.PodIPs = patchStatus.PodIPs
	}
	if patchStatus.StartTime != nil {
		status.StartTime = patchStatus.StartTime
	}
	if len(patchStatus.QOSClass) != 0 {
		status.QOSClass = patchStatus.QOSClass
	}
	status.Conditions = mergeCondition(status.Conditions, patchStatus.Conditions)
	status.InitContainerStatuses = mergeContainerStatus(status.InitContainerStatuses, patchStatus.InitContainerStatuses)
	status.ContainerStatuses = mergeContainerStatus(status.ContainerStatuses, patchStatus.ContainerStatuses)
	status.EphemeralContainerStatuses = mergeContainerStatus(status.EphemeralContainerStatuses, patchStatus.EphemeralContainerStatuses)
	return *status
}

// mergeCondition 将patchConditions中的数据更新或追加到originConditions中。
// 如果patchConditions中的条件类型已存在于originConditions中，则更新该条件。
// 否则，将新条件追加到originConditions列表末尾。
func mergeCondition(originConditions, patchConditions []corev1.PodCondition) []corev1.PodCondition {
	// 创建一个map来记录originConditions中每个条件类型的索引
	conditionMap := make(map[corev1.PodConditionType]int)
	for i, condition := range originConditions {
		conditionMap[condition.Type] = i
	}

	// 遍历patchConditions
	for _, patchCondition := range patchConditions {
		// 检查条件类型是否已存在于originConditions中
		if index, exists := conditionMap[patchCondition.Type]; exists {
			// 已存在，直接更新该位置的条件
			originConditions[index] = patchCondition
		} else {
			// 不存在，追加到originConditions末尾
			originConditions = append(originConditions, patchCondition)
			// 更新map中的索引
			conditionMap[patchCondition.Type] = len(originConditions) - 1
		}
	}

	return originConditions
}

// mergeContainerStatus 将patchContainerStatus中的数据更新或追加到originContainerStatus中。
// 如果patchContainerStatus中的容器名称已存在于originContainerStatus中，则更新该容器的状态。
// 否则，将新容器状态追加到originContainerStatus列表末尾。
func mergeContainerStatus(originContainerStatus, patchContainerStatus []corev1.ContainerStatus) []corev1.ContainerStatus {
	containerNameIndexMap := make(map[string]int)

	// 建立 originContainerStatus 中容器名到索引的映射
	for i, status := range originContainerStatus {
		containerNameIndexMap[status.Name] = i
	}

	// 遍历 patchContainerStatus
	for _, patchStatus := range patchContainerStatus {
		// 检查容器名称是否已存在于 originContainerStatus 中
		if index, exists := containerNameIndexMap[patchStatus.Name]; exists {
			// 已存在，更新对应的容器状态，注意：此处不需更新映射表，因为我们后续操作并不依赖于更新后的索引值
			originContainerStatus[index] = patchStatus
		} else {
			// 不存在，追加到 originContainerStatus 末尾
			originContainerStatus = append(originContainerStatus, patchStatus)
			// 在追加新容器状态后，更新映射表中的索引，以备后续可能的查询或操作使用
			containerNameIndexMap[patchStatus.Name] = len(originContainerStatus) - 1
		}
	}

	return originContainerStatus
}
