package relay

import "sync"

// probeByChannel 在一轮批量测活内按渠道串行，渠道间最多 probeConcurrency 路并发。
// 按输入下标写回结果，避免完成时序打乱界面行顺序；同渠道也保留输入顺序。
func probeByChannel(channelIDs []int, run func(index int) ProbeResult) []ProbeResult {
	batches := make(map[int][]int)
	order := make([]int, 0)
	for index, channelID := range channelIDs {
		if _, exists := batches[channelID]; !exists {
			order = append(order, channelID)
		}
		batches[channelID] = append(batches[channelID], index)
	}

	results := make([]ProbeResult, len(channelIDs))
	slots := make(chan struct{}, probeConcurrency)
	var wg sync.WaitGroup
	for _, channelID := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			for _, index := range batches[channelID] {
				results[index] = run(index)
			}
		}()
	}
	wg.Wait()
	return results
}
