package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/charmbracelet/log"
)

const (
	TaskPriceUpdate = "price_update"
	TaskStatsSave   = "stats_save"
	TaskCleanLLM    = "clean_llm"
	// TaskScheduledProbe 是定时测活的调度节拍, 与其余任务不同, 它的间隔不由设置决定, 也不随设置变化。
	TaskScheduledProbe = "scheduled_probe"
)

func Init() {
	// 定时测活调度器最先注册: 它不读任何设置, 而下面几处读设置失败会提前 return,
	// 排在后面就会因为一个无关的设置读失败而整条监控失效。
	Register(TaskScheduledProbe, relay.ScheduledProbeTickInterval, false, relay.ScheduledProbeTick)

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

	// 注册统计保存任务
	statsSaveIntervalMinutes, err := op.SettingGetInt(model.SettingKeyStatsSaveInterval)
	if err != nil {
		log.Warnf("failed to get stats save interval: %v", err)
		return
	}
	statsSaveInterval := time.Duration(statsSaveIntervalMinutes) * time.Minute
	Register(TaskStatsSave, statsSaveInterval, false, op.StatsSaveDBTask)
}
