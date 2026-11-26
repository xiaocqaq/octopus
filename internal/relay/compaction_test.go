package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCompactResponsesBody(t *testing.T) {
	input := []any{
		map[string]any{"role": "developer", "content": "Keep this instruction"},
	}
	for i := 0; i < 100; i++ {
		input = append(input, map[string]any{"role": "user", "content": fmt.Sprintf("question %d", i)})
		input = append(input, map[string]any{"role": "assistant", "content": fmt.Sprintf("answer %d", i)})
	}
	input = append(input, map[string]any{"role": "user", "content": "active question"}, map[string]any{"type": "function_call_output", "call_id": "active_call", "output": "active result"})
	body, err := json.Marshal(map[string]any{
		"model": "test", "stream": true, "store": true, "previous_response_id": "resp_old", "input": input,
	})
	if err != nil {
		t.Fatal(err)
	}
	var summaries []string
	got, changed, err := compactResponsesBody(body, func(request []byte) (string, error) {
		if strings.Contains(string(request), "previous_response_id") {
			t.Fatal("旧响应引用不应进入摘要请求")
		}
		var payload struct {
			Input        []map[string]any `json:"input"`
			Instructions string           `json:"instructions"`
		}
		if err := json.Unmarshal(request, &payload); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, item := range payload.Input {
			if item["role"] == "assistant" {
				count++
			}
		}
		if count > responsesSummaryChunkTurns {
			t.Fatalf("summary chunk has %d assistant turns", count)
		}
		if payload.Instructions == "" {
			t.Fatal("summary request must override the original task instructions")
		}
		summary := fmt.Sprintf("summary %d", len(summaries)+1)
		summaries = append(summaries, summary)
		return summary, nil
	})
	if err != nil || !changed {
		t.Fatalf("compaction failed: changed=%v err=%v", changed, err)
	}
	var compacted map[string]any
	if err := json.Unmarshal(got, &compacted); err != nil {
		t.Fatal(err)
	}
	if compacted["previous_response_id"] != nil || compacted["store"] != false {
		t.Fatalf("compacted request retained server-side state: %s", got)
	}
	items := compacted["input"].([]any)
	if len(summaries) != 2 || items[0].(map[string]any)["content"] != "Keep this instruction" {
		t.Fatalf("unexpected compacted input: %s", got)
	}
	if got := responsesAssistantTurns(items); got != 30 {
		t.Fatalf("retained assistant turns=%d want=30", got)
	}
	if strings.Contains(string(got), "question 69") || !strings.Contains(string(got), "question 70") || !strings.Contains(string(got), "answer 99") {
		t.Fatalf("compaction did not retain exactly the latest 30 turns: %s", got)
	}
	if items[len(items)-2].(map[string]any)["content"] != "active question" || items[len(items)-1].(map[string]any)["call_id"] != "active_call" {
		t.Fatalf("active turn was not preserved: %s", got)
	}
}

func TestCompactResponsesBodySkipsBelowThresholdAndPreservesHistoryOnSummaryFailure(t *testing.T) {
	input := make([]any, 0, responsesCompactAtTurns*2)
	for i := 0; i < responsesCompactAtTurns-1; i++ {
		input = append(input, map[string]any{"role": "assistant", "content": "answer"})
	}
	input = append(input, map[string]any{"role": "user", "content": "question"})
	body, _ := json.Marshal(map[string]any{"input": input})
	if got, changed, err := compactResponsesBody(body, nil); err != nil || changed || string(got) != string(body) {
		t.Fatalf("below-threshold body changed: changed=%v err=%v", changed, err)
	}

	input = append(input[:len(input)-1], map[string]any{"role": "assistant", "content": "last answer"}, map[string]any{"role": "user", "content": "question"})
	body, _ = json.Marshal(map[string]any{"input": input})
	got, changed, err := compactResponsesBody(body, func([]byte) (string, error) { return "", errors.New("summary failed") })
	if err != nil || !changed {
		t.Fatalf("failed summary did not fall back to a transcript: changed=%v err=%v", changed, err)
	}
	_, items, err := decodeResponsesInput(got)
	if err != nil || responsesModelTurns(items) != 0 || len(items) != 2 {
		t.Fatalf("fallback transcript did not reduce model turns: %s err=%v", got, err)
	}
	transcript, _ := json.Marshal(input[:len(input)-1])
	if !strings.HasSuffix(items[0].(map[string]any)["content"].(string), string(transcript)) || items[1].(map[string]any)["content"] != "question" {
		t.Fatalf("fallback transcript lost conversation content: %s", got)
	}
}

func TestCompactResponsesBodyCountsFunctionCallsAsTurns(t *testing.T) {
	input := make([]any, 0, responsesCompactAtTurns*2+1)
	for i := 0; i < responsesCompactAtTurns; i++ {
		input = append(input,
			map[string]any{"role": "user", "content": "run tool"},
			map[string]any{"type": "function_call", "call_id": fmt.Sprintf("call_%d", i), "name": "probe", "arguments": "{}"},
			map[string]any{"type": "function_call_output", "call_id": fmt.Sprintf("call_%d", i), "output": "ok"},
		)
	}
	input = append(input, map[string]any{"role": "user", "content": "active task"})
	body, err := json.Marshal(map[string]any{"input": input})
	if err != nil {
		t.Fatal(err)
	}
	var summaryCalls int
	compacted, changed, err := compactResponsesBody(body, func([]byte) (string, error) {
		summaryCalls++
		return "tool handoff", nil
	})
	if err != nil || !changed || summaryCalls == 0 {
		t.Fatalf("function-call turns were not compacted: changed=%v summaries=%d err=%v", changed, summaryCalls, err)
	}
	var got struct {
		Input []any `json:"input"`
	}
	if err := json.Unmarshal(compacted, &got); err != nil {
		t.Fatal(err)
	}
	if responsesModelTurns(got.Input) >= responsesCompactAtTurns {
		t.Fatalf("compacted request still has %d model turns", responsesModelTurns(got.Input))
	}
}

func TestCompactResponsesBodyPreservesEarlyFactsWhenSummaryFails(t *testing.T) {
	input := make([]any, 0, 82*2+2)
	input = append(input, map[string]any{"role": "developer", "content": "Keep task context"}, map[string]any{"role": "user", "content": "Early identifier FALLBACK-827"})
	for i := 0; i < responsesCompactAtTurns+10; i++ {
		input = append(input,
			map[string]any{"role": "user", "content": fmt.Sprintf("question %d", i)},
			map[string]any{"role": "assistant", "content": fmt.Sprintf("answer %d", i)},
		)
	}
	input = append(input, map[string]any{"role": "user", "content": "active question"})
	body, err := json.Marshal(map[string]any{"model": "test", "input": input, "previous_response_id": "old", "store": true})
	if err != nil {
		t.Fatal(err)
	}
	compacted, changed, err := compactResponsesBody(body, func([]byte) (string, error) { return "", errors.New("upstream_error") })
	if err != nil || !changed {
		t.Fatalf("fallback compaction failed: changed=%v err=%v", changed, err)
	}
	var got map[string]any
	if err := json.Unmarshal(compacted, &got); err != nil {
		t.Fatal(err)
	}
	items := got["input"].([]any)
	if got["previous_response_id"] != nil || got["store"] != false {
		t.Fatalf("fallback retained server-side state: %s", compacted)
	}
	if !strings.Contains(string(compacted), "Early identifier FALLBACK-827") || items[0].(map[string]any)["content"] != "Keep task context" {
		t.Fatalf("fallback lost earlier facts or developer instructions: %s", compacted)
	}
	if responsesModelTurns(items) != responsesRetainedTurns || items[len(items)-1].(map[string]any)["content"] != "active question" {
		t.Fatalf("fallback retained wrong history: turns=%d body=%s", responsesModelTurns(items), compacted)
	}
}

func TestIsResponsesSessionTurnLimitError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{{errors.New("session_turn_limit_exceeded"), true}, {errors.New("rate limit exceeded"), false}, {nil, false}} {
		if got := isResponsesSessionTurnLimitError(tc.err); got != tc.want {
			t.Errorf("isResponsesSessionTurnLimitError(%v)=%v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestRotateResponsesSession(t *testing.T) {
	body := []byte(`{"prompt_cache_key":"old","client_metadata":{"session_id":"old","thread_id":"old"}}`)
	headers := http.Header{"Session-Id": []string{"old"}, "Thread-Id": []string{"old"}}
	rotated, changed, err := rotateResponsesSession(body, headers)
	if err != nil || !changed {
		t.Fatalf("rotate session failed: changed=%v err=%v", changed, err)
	}
	var payload map[string]any
	if err := json.Unmarshal(rotated, &payload); err != nil {
		t.Fatal(err)
	}
	newID, _ := payload["prompt_cache_key"].(string)
	if newID == "" || newID == "old" || headers.Get("Session-Id") != newID || headers.Get("Thread-Id") != newID {
		t.Fatalf("session identifiers were not consistently rotated: %s %#v", rotated, headers)
	}
}
