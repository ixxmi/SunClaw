package agent

import (
	"strings"
	"testing"
)

func TestRunAnnounceFlow_UsesOutcomeResultAsFindings(t *testing.T) {
	var captured string
	ann := NewSubagentAnnouncer(func(sessionKey, message string) error {
		captured = message
		return nil
	})

	started := int64(1000)
	ended := int64(2500)
	err := ann.RunAnnounceFlow(&SubagentAnnounceParams{
		ChildSessionKey:     "agent:main:subagent:1",
		ChildRunID:          "run-1",
		RequesterSessionKey: "feishu:acc:chat",
		Task:                "ORIGINAL_TASK_SHOULD_NOT_BE_FINDINGS",
		Label:               "status-check",
		StartedAt:           &started,
		EndedAt:             &ended,
		Outcome: &SubagentRunOutcome{
			Status: "ok",
			Result: "FINAL_RESULT_SHOULD_BE_FINDINGS",
		},
		AnnounceType: SubagentAnnounceTypeTask,
	})
	if err != nil {
		t.Fatalf("RunAnnounceFlow error: %v", err)
	}

	if !strings.Contains(captured, "执行结果：\nFINAL_RESULT_SHOULD_BE_FINDINGS") {
		t.Fatalf("expected findings to use outcome result, got: %s", captured)
	}
	if strings.Contains(captured, "执行结果：\nORIGINAL_TASK_SHOULD_NOT_BE_FINDINGS") {
		t.Fatalf("expected task not used as findings when outcome result exists, got: %s", captured)
	}
}

func TestRunAnnounceFlow_EmptySuccessfulResultTellsMainAgentToReplyDone(t *testing.T) {
	var captured string
	ann := NewSubagentAnnouncer(func(sessionKey, message string) error {
		captured = message
		return nil
	})

	err := ann.RunAnnounceFlow(&SubagentAnnounceParams{
		ChildSessionKey:     "agent:main:subagent:2",
		ChildRunID:          "run-2",
		RequesterSessionKey: "wework:default:chat-1",
		Task:                "check status",
		Outcome: &SubagentRunOutcome{
			Status: "ok",
			Result: "",
		},
		AnnounceType: SubagentAnnounceTypeTask,
	})
	if err != nil {
		t.Fatalf("RunAnnounceFlow error: %v", err)
	}

	if !strings.Contains(captured, "该子任务已执行完毕，但没有返回任何输出。") {
		t.Fatalf("expected empty-result placeholder, got: %s", captured)
	}
	if !strings.Contains(captured, "请直接向用户回复：执行完毕") {
		t.Fatalf("expected completion hint for empty result, got: %s", captured)
	}
}
