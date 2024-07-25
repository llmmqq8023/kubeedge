package config

import (
	"sync"

	edgedconfig "github.com/kubeedge/kubeedge/edge/pkg/edged/config"
	metaserverconfig "github.com/kubeedge/kubeedge/edge/pkg/metamanager/metaserver/config"
	"github.com/kubeedge/kubeedge/pkg/apis/componentconfig/edgecore/v1alpha2"
)

var Config Configure
var once sync.Once

type Configure struct {
	v1alpha2.MetaManager
	NodeId string
}

func InitConfigure(m *v1alpha2.MetaManager) {
	once.Do(func() {
		Config = Configure{
			MetaManager: *m,
			NodeId:      edgedconfig.Config.HostnameOverride,
		}
		metaserverconfig.InitConfigure(Config.MetaManager.MetaServer)
	})
}
