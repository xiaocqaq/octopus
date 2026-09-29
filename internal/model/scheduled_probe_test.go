package model

import (
	"testing"
	"time"
)

// at 构造指定星期与整点的时刻, 供时间窗断言使用。
// 星期按界面顺序给(周一=1 ... 周日=7), 掩码也按同一顺序排位, 断言读起来与界面一致。
func at(weekday, hour int, minute int) time.Time {
	// 2026-01-05 是周一, 用它当基准往后推, 避免手工数日历。
	base := time.Date(2026, 1, 5, hour, minute, 0, 0, time.UTC)
	return base.AddDate(0, 0, weekday-1)
}

// TestWindowOpenWithoutMask 没设时间窗(掩码为 0)时任何时刻都算窗口内。
// 这是"选了星期才限时段"的落点: 不选即不限制, 用户不必为了全天可测去把七天全勾上。
func TestWindowOpenWithoutMask(t *testing.T) {
	probe := ScheduledProbe{}
	for weekday := 1; weekday <= 7; weekday++ {
		if !probe.WindowOpen(at(weekday, 13, 0)) {
			t.Fatalf("掩码为 0 时星期 %d 应视为窗口内", weekday)
		}
	}
}

// TestWindowOpenSameDayRange 同日窗口(09:00-18:00)的边界: 起点含, 终点不含。
// 终点取开区间是为了让相邻两个窗口(09-18 与 18-24)能接上而不重叠, 与整点排班的读法一致。
func TestWindowOpenSameDayRange(t *testing.T) {
	probe := ScheduledProbe{Weekdays: WeekdayMonday | WeekdayFriday, StartHour: 9, EndHour: 18}
	cases := []struct {
		name    string
		weekday int
		hour    int
		minute  int
		want    bool
	}{
		{"周一 08:59 窗口外", 1, 8, 59, false},
		{"周一 09:00 含起点", 1, 9, 0, true},
		{"周一 17:59 窗口内", 1, 17, 59, true},
		{"周一 18:00 不含终点", 1, 18, 0, false},
		{"周二未选", 2, 12, 0, false},
		{"周五 12:00 窗口内", 5, 12, 0, true},
		{"周六未选", 6, 12, 0, false},
	}
	for _, tc := range cases {
		if got := probe.WindowOpen(at(tc.weekday, tc.hour, tc.minute)); got != tc.want {
			t.Fatalf("%s: 期望 %v, 却得到 %v", tc.name, tc.want, got)
		}
	}
}

// TestWindowOpenWholeDayWhenHoursEqual 起止整点相同视为整天。
// 界面上 09:00-09:00 若读成"只在 9 点这一小时"会让人意外; 读成整天也让"选了星期但不限时段"有地方可表达。
func TestWindowOpenWholeDayWhenHoursEqual(t *testing.T) {
	probe := ScheduledProbe{Weekdays: WeekdayWednesday, StartHour: 0, EndHour: 0}
	if !probe.WindowOpen(at(3, 0, 0)) || !probe.WindowOpen(at(3, 23, 59)) {
		t.Fatalf("周三 00:00-00:00 应覆盖全天")
	}
	if probe.WindowOpen(at(4, 12, 0)) {
		t.Fatalf("周四未选中, 不该落在窗口内")
	}
}

// TestWindowOpenCrossMidnight 跨午夜窗口(22:00-05:00)按"窗口开始的那一天"查掩码:
// 选了周一至周五, 指的是周一到周五每晚十点开始的那一段, 于是周六凌晨仍算在周五那次窗口内。
// 这是排班语义的自然读法; 若按"当天"查掩码, 用户得把周六也勾上才能覆盖周五夜里。
func TestWindowOpenCrossMidnight(t *testing.T) {
	probe := ScheduledProbe{
		Weekdays:  WeekdayMonday | WeekdayTuesday | WeekdayWednesday | WeekdayThursday | WeekdayFriday,
		StartHour: 22,
		EndHour:   5,
	}
	cases := []struct {
		name    string
		weekday int
		hour    int
		want    bool
	}{
		{"周一 21:59 窗口未开始", 1, 21, false},
		{"周一 22:00 含起点", 1, 22, true},
		{"周一 23:59 深夜段", 1, 23, true},
		{"周二 03:00 属周一那次窗口的凌晨段", 2, 3, true},
		{"周二 05:00 不含终点", 2, 5, false},
		{"周六 03:00 属周五那次窗口的凌晨段", 6, 3, true},
		{"周日 03:00 前一天的周六未选", 7, 3, false},
		{"周日 22:00 周日未选", 7, 22, false},
	}
	for _, tc := range cases {
		if got := probe.WindowOpen(at(tc.weekday, tc.hour, 0)); got != tc.want {
			t.Fatalf("%s: 期望 %v, 却得到 %v", tc.name, tc.want, got)
		}
	}
}

// TestWindowOpenAllWeekdays 全选掩码等价于不限星期, 但仍受时段约束。
func TestWindowOpenAllWeekdays(t *testing.T) {
	probe := ScheduledProbe{Weekdays: WeekdayAll, StartHour: 9, EndHour: 18}
	if !probe.WindowOpen(at(7, 12, 0)) {
		t.Fatalf("七天全选时周日 12:00 应落在窗口内")
	}
	if probe.WindowOpen(at(7, 20, 0)) {
		t.Fatalf("七天全选仍应受时段约束")
	}
}