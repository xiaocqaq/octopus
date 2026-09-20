package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func TestForwardRequestCancellation(t *testing.T) {
	if err := db.InitDB("sqlite", filepath.Join(t.TempDir(), "cancel.db"), false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	resetRoutes()
	t.Cleanup(resetRoutes)
	t.Cleanup(Clear)
	for _, scenario := range []string{"image_wait_headers", "image_wait_body", "image_selection", "chat_selection", "image_success"} {
		t.Run(scenario, func(t *testing.T) {
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if scenario == "image_success" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"data":[{"url":"https://example.test/image.png"}]}`)
					return
				}
				if scenario == "image_wait_body" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				close(entered)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx := context.Background()
			channel, err := op.ChannelCreate(&model.ChannelDetail{
				ChannelConfig: model.ChannelConfig{Name: scenario, Enabled: true, BaseURL: server.URL},
				Keys:          []model.ChannelKeyConfig{{Name: "test", Key: "test-key", Enabled: true}},
				Models:        []string{"model"},
				Grants:        []model.ChannelGrantConfig{{ModelName: "model", KeyName: "test", Protocols: model.ProtocolOpenAIImage}},
			}, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.ChannelDel(channel.ID, ctx)
			var items []model.GroupItemInput
			if !strings.HasSuffix(scenario, "selection") {
				for _, grant := range op.ChannelGrantCandidates() {
					if grant.ChannelID == channel.ID {
						items = append(items, model.GroupItemInput{ChannelGrantID: grant.ID})
					}
				}
			}
			config := model.DefaultGroupRelayConfig()
			config.MemberRetryIntervalSeconds = 60
			group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "cancel-" + scenario, Mode: model.GroupModeFailover, RelayConfig: config, Items: items}, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.GroupDel(group.ID, ctx)
			defer ResetRouteState(group.ID)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			clientCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/test", strings.NewReader(`{"model":"`+group.Name+`","prompt":"test"}`)).WithContext(clientCtx)
			c.Request.Header.Set("Content-Type", "application/json")
			_, updates := OpenRequestStream()
			defer CloseRequestStream(updates)
			done := make(chan struct{})
			go func() {
				defer close(done)
				if scenario == "chat_selection" {
					Forward(llm.APIFormatOpenAIChatCompletion)(c)
				} else {
					ForwardImage("generations")(c)
				}
			}()
			var requestID uint64
			ready := false
			for !ready {
				select {
				case state := <-updates:
					if state.GroupID != group.ID {
						continue
					}
					requestID = state.ID
					switch scenario {
					case "image_selection", "chat_selection":
						ready = len(state.RetryErrors) > 0
					case "image_wait_body":
						ready = state.Status == StatusCommitted
					case "image_wait_headers":
						ready = state.Sending
					case "image_success":
						ready = state.Status == StatusSuccess
					}
				case <-clientCtx.Done():
					t.Fatal("forward did not reach expected state")
				}
			}
			if scenario == "image_wait_headers" {
				select {
				case <-entered:
				case <-clientCtx.Done():
					t.Fatal("image upstream not reached")
				}
			}
			if scenario != "image_success" {
				CancelRequest(requestID)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("request cancellation failed to stop handler")
			}
			mu.Lock()
			state := *requests[requestID]
			mu.Unlock()
			want := StatusCanceled
			if scenario == "image_success" {
				want = StatusSuccess
			}
			if state.Status != want || clientCtx.Err() != nil {
				t.Fatalf("status=%s want=%s client context=%v", state.Status, want, clientCtx.Err())
			}
			if strings.HasSuffix(scenario, "selection") && len(state.RetryErrors) == 0 {
				t.Fatal("selection failure disappeared on cancellation")
			}
		})
	}
}
