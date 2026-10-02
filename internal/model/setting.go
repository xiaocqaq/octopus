package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

type SettingKey string

const (
	SettingKeyProxyURL                SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval       SettingKey = "stats_save_interval"        // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval SettingKey = "model_info_update_interval" // 模型信息更新间隔(小时)
	SettingKeyCORSAllowOrigins        SettingKey = "cors_allow_origins"         // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyModelFilter             SettingKey = "model_filter"              // 渠道获取模型时的全局过滤表达式; 留空表示不过滤
	SettingKeyIQProbePrompt           SettingKey = "iq_probe_prompt"           // 智商探针的题面; 不写死, 界面上可改
	SettingKeyIQProbeAnswer           SettingKey = "iq_probe_answer"           // 智商探针的标准答案; 回答里出现它就算通过
)

// 智商探针的出厂题面与标准答案: 只用来给新库填初值, 也是库里没值时的兜底。
// 两者都能在界面上改(设置入口在模型监控页顶栏, 读写见 internal/relay/iq_probe.go)。
//
// 答案 21 的推导: 形状摸得出来, 所以取的人可以分别决定取几个圆形、几个五角星,
// 于是要找最小的 (a,b), 使得任意 a 个圆形 + b 个五角星里必然出现
// "圆形苹果配五角星桃子" 或 "圆形桃子配五角星苹果"。
// (9,12) 可行: 拿 9 圆 12 星若仍凑不出这两对, 则"有圆苹果 ⟹ 没有星桃子"且"有圆桃子 ⟹ 没有星苹果",
// 星苹果星桃子都缺时五角星最多只剩 4 个西瓜, 与取了 12 个五角星矛盾; 21 = 9 + 12。
// (9,11) 不可行: 对手把圆里只摆苹果桃子(9 个正好取不出西瓜)、星里只摆苹果西瓜(7+4=11)。
// 注意 29 是"一次盲抓"读法的答案, 题面里"不同的形状靠手感可以分辨"正是为了排除该读法,
// 不要把它当成标准答案(两个读法的穷举脚本见 .probe-test/candy.js 与 .probe-test/candy29.py)。
const (
	DefaultIQProbePrompt = "在一个黑色的袋子里放有三种口味的糖果, 每种糖果有两种不同的形状(圆形和五角星形, 不同的形状靠手感可以分辨)。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目, 那么, 最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖?(圆形苹果配五角星桃子, 或圆形桃子配五角星苹果, 均满足要求)圆形糖果: 苹果味 7 个, 桃子味 9 个, 西瓜味 8 个; 五角星形糖果: 苹果味 7 个, 桃子味 6 个, 西瓜味 4 个。只要给我答案, 你不需要任何思考, 我只需要答案的数字。"
	DefaultIQProbeAnswer = "21"
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},       // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: ""},          // CORS 默认不允许跨域，设置为 "*" 才允许所有来源
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"}, // 默认24小时更新一次模型信息
		{Key: SettingKeyModelFilter, Value: ""},               // 默认不过滤模型
		{Key: SettingKeyIQProbePrompt, Value: DefaultIQProbePrompt},
		{Key: SettingKeyIQProbeAnswer, Value: DefaultIQProbeAnswer},
	}
}

func (s *Setting) Validate() error {
	switch s.Key {
	case SettingKeyModelInfoUpdateInterval:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("model info update interval must be an integer")
		}
		return nil
	case SettingKeyModelFilter:
		if s.Value == "" {
			return nil
		}
		// 与渠道侧一致用 ECMAScript 方言校验, 避免设置能存但探测时编译失败。
		if _, err := regexp2.Compile(s.Value, regexp2.ECMAScript); err != nil {
			return fmt.Errorf("model filter regex is invalid: %w", err)
		}
		return nil
	case SettingKeyIQProbePrompt:
		// 题面必须问得出来: 空题面会让探测请求变成一条没有内容的对话, 上游报错反而像模型降智。
		if strings.TrimSpace(s.Value) == "" {
			return fmt.Errorf("iq probe prompt must not be empty")
		}
		return nil
	case SettingKeyIQProbeAnswer:
		// 空答案会让判分永远不通过: 一律判降智, 这是误导而不是宽容。
		if strings.TrimSpace(s.Value) == "" {
			return fmt.Errorf("iq probe answer must not be empty")
		}
		return nil
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":    true,
			"https":   true,
			"socks5":  true,
			"socks5h": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks5, or socks5h")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	}

	return nil
}
