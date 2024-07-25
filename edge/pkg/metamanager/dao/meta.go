package dao

import (
	"encoding/json"
	"fmt"
	"strings"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/klog/v2"

	"github.com/kubeedge/kubeedge/common/constants"
	"github.com/kubeedge/kubeedge/edge/pkg/common/dbm"
	metaManagerConfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/config"
)

// constant metatable name reference
const (
	MetaTableName = "meta"
)

// Meta metadata object
type Meta struct {
	DistributedKey string `orm:"column(distributed_key); size(325); pk"`
	Key            string `orm:"column(key); size(256)"`
	Type           string `orm:"column(type); size(32)"`
	Node           string `orm:"column(node_name); size(64)"`
	Value          string `orm:"column(value); null; type(text)"`
}

// SaveMeta save meta to db
func SaveMeta(meta *Meta) error {
	meta.DistributedKey = metaManagerConfig.Config.NodeId + "/" + meta.Key
	meta.Node = metaManagerConfig.Config.NodeId
	num, err := dbm.DBAccess.Insert(meta)
	klog.V(4).Infof("Insert affected Num: %d, %v", num, err)
	if err == nil || IsNonUniqueNameError(err) {
		return nil
	}
	return err
}

// IsNonUniqueNameError tests if the error returned by sqlite is unique.
// It will check various sqlite versions.
func IsNonUniqueNameError(err error) bool {
	str := err.Error()
	if strings.HasSuffix(str, "are not unique") || strings.Contains(str, "UNIQUE constraint failed") || strings.HasSuffix(str, "constraint failed") {
		return true
	}
	return false
}

// DeleteMetaByKey delete meta by key
func DeleteMetaByKey(key string) error {
	num, err := dbm.DBAccess.QueryTable(MetaTableName).Filter("key", key).Filter("node_name", metaManagerConfig.Config.NodeId).Delete()
	klog.V(4).Infof("Delete affected Num: %d, %v", num, err)
	return err
}

// DeleteMetaByKeyAndPodUID delete meta by key and podUID
func DeleteMetaByKeyAndPodUID(key, podUID string) (int64, error) {
	sqlStr := fmt.Sprintf("DELETE FROM meta WHERE key = '%s' and node_name = '%s' and value LIKE '%%%s%%'", key, metaManagerConfig.Config.NodeId, podUID)
	res, err := dbm.DBAccess.Raw(sqlStr).Exec()
	if err != nil {
		klog.Errorf("delete pod by key %s and podUID %s failed, err: %v", key, podUID, err)
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteMetaByKeyAndNodeId delete meta by key and node id
func DeleteMetaByKeyAndNodeId(key, nodeId string) (int64, error) {
	sqlStr := fmt.Sprintf("DELETE FROM meta WHERE key = '%s' and node_name = '%s'", key, nodeId)
	res, err := dbm.DBAccess.Raw(sqlStr).Exec()
	if err != nil {
		klog.Errorf("delete pod by key %s and nodeId %s failed, err: %v", key, nodeId, err)
		return 0, err
	}
	return res.RowsAffected()
}

// UpdateMeta update meta
func UpdateMeta(meta *Meta) error {
	meta.DistributedKey = metaManagerConfig.Config.NodeId + "/" + meta.Key
	meta.Node = metaManagerConfig.Config.NodeId
	num, err := dbm.DBAccess.Update(meta) // will update all field
	klog.V(4).Infof("Update affected Num: %d, %v", num, err)
	return err
}

// InsertOrUpdate insert or update meta
func InsertOrUpdate(meta *Meta) error {
	distributedKey := metaManagerConfig.Config.NodeId + "/" + meta.Key
	klog.V(3).Infof("DistributedKey: %s", distributedKey)
	_, err := dbm.DBAccess.Raw("INSERT OR REPLACE INTO meta (distributed_key, key, type, node_name, value) VALUES (?,?,?,?,?)", distributedKey, meta.Key, meta.Type, metaManagerConfig.Config.NodeId, meta.Value).Exec() // will update all field
	klog.V(3).Infof("Update error %v of resource: %#v", err, meta)
	return err
}

// UpdateMetaField update special field
func UpdateMetaField(key string, col string, value interface{}) error {
	updatemap := map[string]interface{}{col: value}
	// If the pk is updated
	if col == "key" {
		updatemap["distributed_key"] = fmt.Sprintf("%s/%v", metaManagerConfig.Config.NodeId, value)
		klog.Infof("In UpdateMetaField: distributed_key changed on node %s from %v to %v", metaManagerConfig.Config.NodeId, key, value)
	}
	num, err := dbm.DBAccess.QueryTable(MetaTableName).Filter("key", key).Filter("node_name", metaManagerConfig.Config.NodeId).Update(updatemap)
	klog.V(4).Infof("Update affected Num: %d, %v", num, err)
	return err
}

// UpdateMetaFields update special fields
func UpdateMetaFields(key string, cols map[string]interface{}) error {
	if newkey, ok := cols["key"]; ok {
		cols["distributed_key"] = fmt.Sprintf("%s/%v", metaManagerConfig.Config.NodeId, newkey)
		klog.Infof("In UpdateMetaFields: distributed_key changed on node %s from %v to %v", metaManagerConfig.Config.NodeId, key, newkey)
	}
	num, err := dbm.DBAccess.QueryTable(MetaTableName).Filter("key", key).Filter("node_name", metaManagerConfig.Config.NodeId).Update(cols)
	klog.V(4).Infof("Update affected Num: %d, %v", num, err)
	return err
}

// QueryMeta return only meta's value, if no error, Meta not null
func QueryMeta(key string, condition string) (*[]string, error) {
	meta := new([]Meta)
	_, err := dbm.DBAccess.QueryTable(MetaTableName).Filter(key, condition).Filter("node_name", metaManagerConfig.Config.NodeId).All(meta)
	if err != nil {
		return nil, err
	}

	var result []string
	for _, v := range *meta {
		result = append(result, v.Value)
	}
	return &result, nil
}

// QueryAllMeta return all meta, if no error, Meta not null
func QueryAllMeta(key string, condition string) (*[]Meta, error) {
	meta := new([]Meta)
	_, err := dbm.DBAccess.QueryTable(MetaTableName).Filter(key, condition).Filter("node_name", metaManagerConfig.Config.NodeId).All(meta)
	if err != nil {
		return nil, err
	}

	return meta, nil
}

// QueryAllMetas return all metas from distributed database, if no error, Meta not null
func QueryAllMetas(key string, condition string) (*[]Meta, error) {
	metas := new([]Meta)
	_, err := dbm.DBAccess.QueryTable(MetaTableName).Filter(key, condition).All(metas)
	if err != nil {
		return nil, err
	}

	return metas, nil
}

// QueryMetasByNodeId return some one node's metas from distributed database, if no error, Meta not null
func QueryMetasByNodeId(key, condition, nodeId string) (*[]Meta, error) {
	metas := new([]Meta)
	var err error
	if len(nodeId) != 0 {
		_, err = dbm.DBAccess.QueryTable(MetaTableName).Filter(key, condition).Filter("node_name", nodeId).All(metas)
	} else {
		_, err = dbm.DBAccess.QueryTable(MetaTableName).Filter(key, condition).All(metas)
	}
	if err != nil {
		return nil, err
	}

	return metas, nil
}

// SaveMQTTMeta saves mqtt container data in sqlites
// When egdecore starts, edged will start mqtt container
func SaveMQTTMeta(nodeName string) error {
	flag := true
	mqttData := coreV1.Pod{
		TypeMeta: metaV1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metaV1.ObjectMeta{
			Name:      constants.DeafultMosquittoContainerName,
			Namespace: "default",
			UID:       uuid.NewUUID(),
		},
		Spec: coreV1.PodSpec{
			Containers: []coreV1.Container{
				{
					Name:  "mqtt",
					Image: constants.DefaultMosquittoImage,
					Ports: []coreV1.ContainerPort{
						{
							ContainerPort: 1883,
							HostPort:      1883,
							Protocol:      coreV1.ProtocolTCP,
						}, {
							ContainerPort: 9001,
							HostPort:      9001,
							Protocol:      coreV1.ProtocolTCP,
						},
					},
					VolumeMounts: []coreV1.VolumeMount{
						{
							MountPath: "/mosquitto",
							Name:      "mqtt-path",
						},
					},
				},
			},
			Volumes: []coreV1.Volume{
				{
					Name: "mqtt-path",
					VolumeSource: coreV1.VolumeSource{
						HostPath: &coreV1.HostPathVolumeSource{
							Path: "/var/lib/kubeedge/mqtt",
						},
					},
				},
			},
			NodeName:           nodeName,
			RestartPolicy:      coreV1.RestartPolicyAlways,
			DNSPolicy:          coreV1.DNSClusterFirst,
			EnableServiceLinks: &flag,
		},
	}
	mqttDataStr, _ := json.Marshal(mqttData)
	mqttMeta := Meta{
		Key:   fmt.Sprintf("default/pod/%s", constants.DeafultMosquittoContainerName),
		Type:  "pod",
		Value: string(mqttDataStr),
	}
	err := SaveMeta(&mqttMeta)
	if err != nil {
		return err
	}

	return nil
}
