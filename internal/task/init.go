package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/charmbracelet/log"
)

const (
	TaskPriceUpdate  = "price_update"
	TaskStatsSave    = "stats_save"
	TaskSyncLLM      = "sync_llm"
	TaskCleanLLM     = "clean_llm"
	TaskGroupHealth  = "group_health"
)

func Init() {
	priceUpdateIntervalHours, err := op.SettingGetInt(model.SettingKeyModelInfoUpdateInterval)
	if err != nil {
		log.Errorf("failed to get model info update interval: %v", err)
		return
	}
	priceUpdateInterval := time.Duration(priceUpdateIntervalHours) * time.Hour
	// 注册价格更新任务
	Register(string(model.SettingKeyModelInfoUpdateInterval), priceUpdateInterval, true, func() {
		if err := price.UpdateLLMPrice(context.Background()); err != nil {
			log.Warnf("failed to update price info: %v", err)
		}
	})

	// 注册LLM同步任务
	syncLLMIntervalHours, err := op.SettingGetInt(model.SettingKeySyncLLMInterval)
	if err != nil {
		log.Warnf("failed to get sync LLM interval: %v", err)
		return
	}
	syncLLMInterval := time.Duration(syncLLMIntervalHours) * time.Hour
	Register(string(model.SettingKeySyncLLMInterval), syncLLMInterval, true, SyncModelsTask)

	// 注册统计保存任务
	statsSaveIntervalMinutes, err := op.SettingGetInt(model.SettingKeyStatsSaveInterval)
	if err != nil {
		log.Warnf("failed to get stats save interval: %v", err)
		return
	}
	statsSaveInterval := time.Duration(statsSaveIntervalMinutes) * time.Minute
		Register(TaskStatsSave, statsSaveInterval, false, op.StatsSaveDBTask)

		// 注册分组健康任务：每小时测试所有分组的模型项，失效项自动移出分组，
		// 使负载均衡只在可用的模型项上进行。（注意：不随启动运行，
		// 避免配置/网络异常时启动即刻清空分组池）
		Register(TaskGroupHealth, time.Hour, false, func() {
			if err := op.TestAllGroupsAndPrune(context.Background()); err != nil {
				log.Warnf("group health task failed: %v", err)
			}
		})
	}
