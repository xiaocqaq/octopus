package relay

import (
	"bytes"
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
		var transcript struct{ Input []map[string]any }
		text := payload.Input[0]["content"].(string)
		_, text, ok := strings.Cut(text, "\n")
		if !ok {
			t.Fatal("missing summary transcript")
		}
		if err := json.Unmarshal([]byte(text), &transcript); err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, item := range transcript.Input {
			if item["role"] == "assistant" {
				count++
			}
		}
		if count != 70 {
			t.Fatalf("single summary should contain all 70 selected turns, got %d", count)
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
	if len(summaries) != 1 || items[0].(map[string]any)["content"] != "Keep this instruction" {
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
	if err != nil || !changed || summaryCalls != 1 {
		t.Fatalf("compact small tool history in one summary: changed=%v summaries=%d err=%v", changed, summaryCalls, err)
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

func TestCompactResponsesBodySummarizesOnlyRecentHistoryWhenOversized(t *testing.T) {
	input := []any{map[string]any{"role": "developer", "content": "FIXED INSTRUCTIONS"}}
	for i := 0; i < 90; i++ {
		input = append(input,
			map[string]any{"role": "user", "content": fmt.Sprintf("HISTORY-%03d", i)},
			map[string]any{"role": "assistant", "content": strings.Repeat(`\`, 10<<10)},
		)
	}
	input = append(input, map[string]any{"role": "user", "content": "CURRENT QUESTION"})
	body, _ := json.Marshal(map[string]any{"input": input})
	calls := 0
	compacted, changed, err := compactResponsesBody(body, func(request []byte) (string, error) {
		calls++
		if len(request) > responsesSummaryMaxInputBytes {
			t.Errorf("encoded summary body has %d bytes, limit %d", len(request), responsesSummaryMaxInputBytes)
		}
		if strings.Contains(string(request), "HISTORY-000") || !strings.Contains(string(request), "HISTORY-059") || !strings.Contains(string(request), "FIXED INSTRUCTIONS") {
			t.Error("summary must omit the oldest history and include the newest eligible history and instructions")
		}
		return "", errors.New("summary unavailable")
	})
	if err != nil || !changed || calls != 1 {
		t.Fatalf("expected one summary attempt: changed=%v calls=%d err=%v", changed, calls, err)
	}
	_, output, err := decodeResponsesInput(compacted)
	if err != nil || responsesAssistantTurns(output) != 30 {
		t.Fatalf("recent structured history was not retained: %v", err)
	}
	if strings.Contains(string(compacted), "HISTORY-000") || !strings.Contains(string(compacted), "HISTORY-059") || !strings.Contains(string(compacted), "CURRENT QUESTION") {
		t.Fatal("fallback must keep the selected history and current question without restoring discarded history")
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

func TestCompactResponsesBodyRejectsHistoryWhenNoSummarySuffixFits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []any
	}{
		{
			name: "single oversized turn",
			input: func() []any {
				items := []any{map[string]any{"role": "user", "content": strings.Repeat("x", responsesSummaryMaxInputBytes)}}
				for i := 0; i < responsesCompactAtTurns; i++ {
					items = append(items, map[string]any{"role": "assistant", "content": "answer"})
				}
				items = append(items, map[string]any{"role": "user", "content": "active"})
				return items
			}(),
		},
		{
			name: "fixed instructions exceed limit",
			input: func() []any {
				items := []any{map[string]any{"role": "developer", "content": strings.Repeat("x", responsesSummaryMaxInputBytes)}}
				for i := 0; i < responsesCompactAtTurns; i++ {
					items = append(items, map[string]any{"role": "user", "content": "question"}, map[string]any{"role": "assistant", "content": "answer"})
				}
				items = append(items, map[string]any{"role": "user", "content": "active"})
				return items
			}(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"input": tc.input})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			got, changed, err := compactResponsesBody(body, func([]byte) (string, error) { calls++; return "summary", nil })
			if err == nil || changed || calls != 0 || !bytes.Equal(got, body) {
				t.Fatalf("unsafe compaction: changed=%v calls=%d err=%v bodyPreserved=%v", changed, calls, err, bytes.Equal(got, body))
			}
		})
	}
}
