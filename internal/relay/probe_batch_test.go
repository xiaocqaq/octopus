package relay

import (
	"sync"
	"testing"
	"time"
)

func TestProbeByChannelSerializesChannelAndPreservesOrder(t *testing.T) {
	var mu sync.Mutex
	active := make(map[int]int)
	maxActive := make(map[int]int)
	globalMax := 0

	results := probeByChannel([]int{1, 1, 2}, func(index int) ProbeResult {
		channelID := []int{1, 1, 2}[index]
		mu.Lock()
		active[channelID]++
		if active[channelID] > maxActive[channelID] {
			maxActive[channelID] = active[channelID]
		}
		total := 0
		for _, count := range active {
			total += count
		}
		if total > globalMax {
			globalMax = total
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		active[channelID]--
		mu.Unlock()
		return ProbeResult{ItemID: index}
	})

	if got := []int{results[0].ItemID, results[1].ItemID, results[2].ItemID}; got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("结果顺序应与输入一致, actual %v", got)
	}
	if maxActive[1] != 1 || maxActive[2] != 1 {
		t.Fatalf("同渠道必须串行, maxActive=%v", maxActive)
	}
	if globalMax < 2 {
		t.Fatalf("不同渠道应允许并发, globalMax=%d", globalMax)
	}
}
