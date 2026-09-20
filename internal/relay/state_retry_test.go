package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

func TestRetryErrorsSurviveRetryAndFinish(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%v", canceled), func(t *testing.T) {
			r := newRequestState(context.Background(), "retry", 0, model.ProtocolOpenAIChatCompletion, `{}`, 0)
			t.Cleanup(Clear)
			r.startRound(nil, "manual-provider", "upstream-model", model.ProtocolOpenAIChatCompletion)
			r.finishRound("upstream responded 401: invalid key")
			r.finishRound("upstream responded 401: invalid key")
			want := []RetryError{{Round: 1, TargetChannel: "manual-provider", TargetModel: "upstream-model", Error: "upstream responded 401: invalid key"}}
			r.startRound(nil, "manual-provider", "upstream-model", model.ProtocolOpenAIChatCompletion)
			if r.Error != "" || !reflect.DeepEqual(r.RetryErrors, want) {
				t.Fatalf("retry lost history: %+v", r)
			}
			if canceled {
				CancelRequest(r.ID)
				r.markCanceled(r.requestCtx.Err(), "", nil)
			} else {
				r.finishRound("")
				r.markCommitted(false)
				r.markSucceeded("ok", nil)
				// finishLocked cancels resources; that cancellation must not rewrite success.
				r.markCanceled(context.Canceled, "", nil)
				r.markFailed(context.Canceled, "", nil)
				CancelRequest(r.ID)
			}
			wantStatus := StatusSuccess
			if canceled {
				wantStatus = StatusCanceled
			}
			if r.Status != wantStatus || !reflect.DeepEqual(r.RetryErrors, want) {
				t.Fatalf("terminal status/history: %+v", r)
			}
			encoded, err := json.Marshal(r)
			if err != nil || !strings.Contains(string(encoded), `"retry_errors":[{"round":1,"target_channel":"manual-provider","target_model":"upstream-model","error":"upstream responded 401: invalid key"}]`) {
				t.Fatalf("JSON=%s err=%v", encoded, err)
			}
		})
	}
}

func TestRetryErrorsBoundAndImmutableSnapshots(t *testing.T) {
	r := newRequestState(context.Background(), "history", 0, 0, "", 0)
	t.Cleanup(func() { r.markCanceled(context.Canceled, "", nil); Clear() })
	encoded, err := json.Marshal(r)
	if err != nil || strings.Contains(string(encoded), "retry_errors") {
		t.Fatalf("empty history should be omitted: %s, %v", encoded, err)
	}
	for i := 1; i <= maxRetryErrors; i++ {
		r.startRound(nil, "provider", "model", 0)
		r.finishRound(fmt.Sprintf("failure %d", i))
	}
	snapshot, updates := OpenRequestStream()
	defer CloseRequestStream(updates)
	var saved RequestState
	for _, state := range snapshot {
		if state.ID == r.ID {
			saved = state
		}
	}
	wantSaved := append([]RetryError(nil), saved.RetryErrors...)
	r.finishRound("another error in the same round")
	published := <-updates
	wantPublished := append([]RetryError(nil), published.RetryErrors...)
	// Read old snapshots while writes append and trim; go test -race checks backing-array sharing.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			_, _ = json.Marshal(saved)
			_, _ = json.Marshal(published)
		}
	}()
	for i := 21; i <= 45; i++ {
		r.startRound(nil, "provider", "model", 0)
		r.finishRound(fmt.Sprintf("failure %d", i))
	}
	<-done
	if len(r.RetryErrors) != 20 || r.RetryErrors[0].Round != 26 || r.RetryErrors[19].Round != 45 {
		t.Fatalf("wrong history boundary: %+v", r.RetryErrors)
	}
	if !reflect.DeepEqual(saved.RetryErrors, wantSaved) || !reflect.DeepEqual(published.RetryErrors, wantPublished) {
		t.Fatal("published history was mutated")
	}
}

func TestSelectionErrorsAndCancelWait(t *testing.T) {
	r := newRequestState(context.Background(), "waiting", 0, 0, "", 0)
	t.Cleanup(Clear)
	r.startRound(nil, "previous", "previous-model", 0)
	r.finishRound("previous failure")
	r.failSelection("no active member in manual mode")
	failure := r.RetryErrors[1]
	if failure.Round != 2 || failure.TargetChannel != "" || failure.TargetModel != "" || failure.Error != r.Error {
		t.Fatalf("selection failure attributed to previous target: %+v", failure)
	}
	done := make(chan bool, 1)
	go func() { done <- r.wait(r.requestCtx, 60) }()
	CancelRequest(r.ID)
	select {
	case continued := <-done:
		if continued || r.Status != StatusCanceled || len(r.RetryErrors) != 2 {
			t.Fatalf("wait cancellation lost state: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not interrupt wait")
	}
}

func TestFirstTokenDurationIncludesRoutingAndRetries(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%v", streaming), func(t *testing.T) {
			r := newRequestState(context.Background(), "timing", 0, 0, "", 0)
			t.Cleanup(func() { r.markCanceled(context.Canceled, "", nil); Clear() })
			r.StartedAt = time.Now().Add(-10 * time.Second)
			r.startRound(nil, "failed", "model", 0)
			r.finishRound("failed")
			r.startRound(nil, "successful", "model", 0)
			r.RoundStartedAt = time.Now().Add(-time.Second)
			r.markCommitted(streaming)
			if r.FirstTokenDuration < 10*time.Second || r.FirstTokenDuration > 11*time.Second {
				t.Fatalf("first token excludes earlier attempts: %v", r.FirstTokenDuration)
			}
			if streaming {
				r.addOutput()
				r.finishStream()
				if r.ResponseDuration != 0 || r.StreamDuration <= 0 || r.OutputChars != 1 {
					t.Fatalf("stream timing/output: %+v", r)
				}
			} else if r.StreamDuration != 0 || r.ResponseDuration < time.Second || r.ResponseDuration > 2*time.Second {
				t.Fatalf("response timing: %+v", r)
			}
		})
	}
}
