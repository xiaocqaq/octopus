package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestNormalizeChannelSummaryTimeout(t *testing.T) {
	for _, seconds := range []int{0, 1, 600, 86400, -1, 86401} {
		config, err := normalizeChannelConfig(model.ChannelConfig{Name: "test", BaseURL: "https://example.invalid", ResponsesSummaryTimeoutSeconds: seconds})
		if seconds < 0 || seconds > 86400 {
			if err == nil {
				t.Errorf("accepted invalid summary timeout %d", seconds)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		want := seconds
		if want == 0 {
			want = 120
		}
		if config.ResponsesSummaryTimeoutSeconds != want {
			t.Errorf("summary timeout=%d want=%d", config.ResponsesSummaryTimeoutSeconds, want)
		}
	}
}
