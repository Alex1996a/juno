package confgov2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/douyu/juno/pkg/util"

	"github.com/douyu/juno/internal/pkg/service/clientproxy"
	"github.com/douyu/juno/pkg/cfg"
	"github.com/douyu/juno/pkg/errorconst"
	"github.com/douyu/juno/pkg/model/db"
	"github.com/douyu/juno/pkg/model/view"
	"github.com/douyu/jupiter/pkg/xlog"
	"go.etcd.io/etcd/clientv3"
	"go.uber.org/zap"
)

func syncUsedStatus(nodes []db.AppNode, resp []view.RespConfigInstanceItem, env, zoneCode, filePath string) ([]view.RespConfigInstanceItem, error) {
	// get junoAgentList
	junoAgentList := assemblyJunoAgent(nodes)
	if len(junoAgentList) > 400 || len(junoAgentList) < 0 {
		return resp, errorconst.JunoAgentQueryOverSize.Error()
	}
	if len(junoAgentList) == 0 {
		return resp, nil
	}
	// use map
	usedMap := make(map[string]int, 0)
	for _, ag := range junoAgentList {
		usedMap[ag.HostName] = 0
		for _, fp := range strings.Split(filePath, ";") {
			if status := getUsedStatus(env, zoneCode, fp, ag.IPPort); status > 0 {
				usedMap[ag.HostName] = status
			}
		}
	}
	for k, v := range resp {
		if resp[k].ConfigFileUsed != 0 {
			continue
		}
		if newState, ok := usedMap[resp[k].HostName]; ok {
			resp[k].ConfigFileUsed = uint(newState)
			mysql.Model(&db.ConfigurationStatus{}).Where("id=?", v.ConfigurationStatusID).Update("used", newState)
		}
	}
	return resp, nil
}

func syncPublishStatus(appName, env string, zoneCode string, configuration db.Configuration, notSyncFlag map[string]db.AppNode, resp []view.RespConfigInstanceItem) ([]view.RespConfigInstanceItem, error) {
	for _, prefix := range cfg.Cfg.Configure.Prefixes {
		newSyncDataMap, err := configurationSynced(appName, env, zoneCode, configuration.Name, configuration.Format, prefix, notSyncFlag)
		if err != nil {
			xlog.Error("syncPublishStatus", zap.String("appName", appName), zap.String("env", env), zap.String("zoneCode", zoneCode), zap.String("prefix", prefix), zap.String("err", err.Error()))
			continue
		}
		xlog.Debug("syncPublishStatus", zap.String("appName", appName), zap.String("env", env), zap.String("zoneCode", zoneCode), zap.Any("newSyncDataMap", newSyncDataMap))

		var version = configuration.Version
		for k, v := range resp {
			if newState, ok := newSyncDataMap[resp[k].HostName]; ok {
				resp[k].Version = newState.Version
				resp[k].ChangeLog = commitMsg(newState.Version, configuration.ID)
				resp[k].SyncAt = util.Timestamp2String64(newState.Timestamp)
				if newState.Version == version {
					resp[k].ConfigFileSynced = 1
					mysql.Model(&db.ConfigurationStatus{}).Where("id=?", v.ConfigurationStatusID).Update("synced", 1)
				}
			}
		}
	}
	return resp, nil
}

func syncTakeEffectStatus(appName, governPort, env string, zoneCode string, configuration db.Configuration, notTakeEffectNodes map[string]db.AppNode, resp []view.RespConfigInstanceItem) ([]view.RespConfigInstanceItem, error) {
	newSyncDataMap, err := configurationTakeEffect(appName, env, zoneCode, configuration.Name, configuration.Format, governPort, notTakeEffectNodes)
	if err != nil {
		return resp, err
	}
	var version = configuration.Version
	for k, v := range resp {
		if resp[k].ConfigFileTakeEffect == 1 {
			continue
		}
		if newState, ok := newSyncDataMap[resp[k].HostName]; ok {
			if newState.EffectVersion == version {
				resp[k].ConfigFileTakeEffect = 1
				mysql.Model(&db.ConfigurationStatus{}).Where("id=?", v.ConfigurationStatusID).Update("take_effect", 1)
			}
		}
	}
	return resp, nil
}

func getUsedStatus(env, zoneCode, filePath string, ipPort string) int {
	// query proxy for used status
	resp, err := clientproxy.ClientProxy.HttpPost(view.UniqZone{
		Env:  env,
		Zone: zoneCode,
	}, view.ReqHTTPProxy{
		Address: ipPort,
		URL:     queryAgentUsedStatus,
		Params: map[string]string{
			"config": filePath,
		},
	})
	if err != nil {
		return 0
	}
	configurationUsedStatus := new(struct {
		Code int `json:"code"`
		Data struct {
			Supervisor bool `json:"supervisor"`
			Systemd    bool `json:"systemd"`
		} `json:"data"`
		Msg string `json:"msg"`
	})
	if err = json.Unmarshal(resp.Body(), configurationUsedStatus); err != nil {
		return 0
	}
	if configurationUsedStatus.Data.Supervisor {
		return view.ConfigureUsedTypeSupervisor
	}
	if configurationUsedStatus.Data.Systemd {
		return view.ConfigureUsedTypeSystemd
	}
	return 0
}

func getConfigurationStatus(configurationID uint, hostName string) (res db.ConfigurationStatus, err error) {
	query := mysql.Preload("ConfigurationPublish").Where("configuration_id=? and host_name=?", configurationID, hostName).Order("id desc", false).Find(&res)
	if query.Error != nil {
		err = query.Error
		return
	}
	return
}

func configurationSynced(appName, env, zoneCode, filename, format, prefix string, notSyncFlag map[string]db.AppNode) (list map[string]view.ConfigurationStatus, err error) {
	list = make(map[string]view.ConfigurationStatus, 0)
	fileNameWithSuffix := fmt.Sprintf("%s.%s", filename, format)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	key := fmt.Sprintf("/%s/callback/%s/%s", prefix, appName, fileNameWithSuffix)
	defer cancel()
	resp, err := clientproxy.ClientProxy.EtcdGet(view.UniqZone{Env: env, Zone: zoneCode}, ctx, key, clientv3.WithPrefix())
	if err != nil {
		xlog.Warn("configurationSynced", zap.String("step", "EtcdGet"), zap.String("appName", appName), zap.String("env", env), zap.String("zoneCode", zoneCode), zap.String("key", key), zap.String("error", err.Error()))
		return
	}
	if len(resp.Kvs) == 0 {
		err = errorconst.ParamConfigCallbackKvIsZero.Error()
		xlog.Warn("configurationSynced", zap.String("step", "resp.Kvs"), zap.String("appName", appName), zap.String("env", env), zap.String("zoneCode", zoneCode), zap.String("key", key), zap.Any("resp", resp))
		return
	}
	// publish status, synced status
	for _, item := range resp.Kvs {
		row := view.ConfigurationStatus{}
		if err := json.Unmarshal(item.Value, &row); err != nil {
			continue
		}
		xlog.Debug("configurationSynced", zap.String("step", "for.resp.Kvs"), zap.Any("row", row), zap.Any("notSyncFlag", notSyncFlag))

		if _, ok := notSyncFlag[row.Hostname]; !ok {
			continue
		}
		list[row.Hostname] = row
	}
	xlog.Debug("configurationSynced", zap.String("step", "finish"), zap.Any("list", list))

	return
}

func configurationTakeEffect(appName, env, zoneCode, filename, format, governPort string, notTakeEffectNodes map[string]db.AppNode) (list map[string]view.ConfigurationStatus, err error) {
	list = make(map[string]view.ConfigurationStatus, 0)
	// take effect status
	// publish status, synced status
	for _, node := range notTakeEffectNodes {
		row := view.ConfigurationStatus{}
		agentQuestResp, agentQuestError := clientproxy.ClientProxy.HttpGet(view.UniqZone{Env: env, Zone: zoneCode}, view.ReqHTTPProxy{
			Address: node.IP + ":" + governPort,
			URL:     cfg.Cfg.ClientProxy.HttpRouter.GovernConfig,
		})
		if agentQuestError != nil {
			err = agentQuestError
			continue
		}
		var out struct {
			JunoConfigurationVersion string `json:"juno_configuration_version"`
			JunoAgentMD5             string `json:"juno_agent_md5"`
		}
		_ = json.Unmarshal(agentQuestResp.Body(), &out)
		effectVersion := out.JunoConfigurationVersion
		row.EffectVersion = effectVersion
		list[node.HostName] = row
	}
	return
}
