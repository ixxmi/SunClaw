package agent

import (
	"testing"
)

func TestShrimpBrainPersistsFullChain(t *testing.T) {
	dir := t.TempDir()
	tracker := NewShrimpBrainTracker(dir)

	runID := tracker.StartMainTask("msg-1", "block-main", "user-main", "session-main", "vibecoding", "cli", "chat-1", "优化前端页面")
	if runID == "" {
		t.Fatalf("expected run id")
	}

	tracker.RecordPrompt("session-main", "vibecoding", false, "main prompt body", []PromptLayerSnapshot{
		{Name: "builtin_boundary", Enabled: true, Source: "builtin_boundary"},
		{Name: "agent_core", Enabled: true, Source: "agent_custom_prompt"},
	})
	tracker.RecordLoopNode("session-main", "vibecoding", false, 1, "tool_calls", "我先拆分并派发前端任务", 1)
	tracker.RecordSubagentDispatchAt("session-main", "agent:frontend:subagent:1", "frontend", "frontend-polish", "优化 Hero 区块", 1)
	tracker.RecordPrompt("agent:frontend:subagent:1", "frontend", true, "frontend prompt body", []PromptLayerSnapshot{
		{Name: "builtin_boundary", Enabled: true, Source: "builtin_boundary"},
		{Name: "subagent_descriptor", Enabled: true, Source: "dynamic_subagent"},
	})
	tracker.RecordLoopNode("agent:frontend:subagent:1", "frontend", true, 1, "tool_calls", "我会先修改样式再局部验证", 1)
	tracker.RecordToolCall("agent:frontend:subagent:1", "frontend", true, 1, "tool-1", "edit_file", map[string]any{"path": "ui/src/App.vue"}, "updated hero styles", "")
	tracker.RecordSubagentResult("agent:frontend:subagent:1", "frontend", "completed", "已完成前端优化", "")
	tracker.RecordMainReply("session-main", "vibecoding", "前端优化已完成并已回收结果")

	reloaded := NewShrimpBrainTracker(dir)
	snapshot := reloaded.Snapshot()
	if len(snapshot.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(snapshot.Runs))
	}

	run := snapshot.Runs[0]
	if run.MainPrompt != "main prompt body" {
		t.Fatalf("expected main prompt persisted, got %q", run.MainPrompt)
	}
	if run.MainReply != "前端优化已完成并已回收结果" {
		t.Fatalf("expected main reply persisted, got %q", run.MainReply)
	}
	if len(run.MainLoops) != 1 {
		t.Fatalf("expected 1 main loop, got %d", len(run.MainLoops))
	}
	mainLoop := run.MainLoops[0]
	if len(mainLoop.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call on main loop, got %d", len(mainLoop.ToolCalls))
	}
	dispatch := mainLoop.ToolCalls[0]
	if dispatch.ChildAgentID != "frontend" {
		t.Fatalf("expected child agent frontend, got %q", dispatch.ChildAgentID)
	}
	if dispatch.ChildPrompt != "frontend prompt body" {
		t.Fatalf("expected child prompt persisted, got %q", dispatch.ChildPrompt)
	}
	if dispatch.ChildReply != "已完成前端优化" {
		t.Fatalf("expected child reply persisted, got %q", dispatch.ChildReply)
	}
	if len(dispatch.ChildLoops) != 1 {
		t.Fatalf("expected child loop persisted, got %d", len(dispatch.ChildLoops))
	}
	childLoop := dispatch.ChildLoops[0]
	if len(childLoop.ToolCalls) != 1 {
		t.Fatalf("expected child tool call persisted, got %d", len(childLoop.ToolCalls))
	}
	if childLoop.ToolCalls[0].ToolName != "edit_file" {
		t.Fatalf("expected child tool edit_file, got %q", childLoop.ToolCalls[0].ToolName)
	}
}

func TestShrimpBrainDeleteRunPersists(t *testing.T) {
	dir := t.TempDir()
	tracker := NewShrimpBrainTracker(dir)
	runID := tracker.StartMainTask("msg-2", "block-delete", "user-delete", "session-delete", "vibecoding", "cli", "chat-2", "删除测试")
	if runID == "" {
		t.Fatalf("expected run id")
	}
	if !tracker.DeleteRun(runID) {
		t.Fatalf("expected delete to succeed")
	}

	reloaded := NewShrimpBrainTracker(dir)
	snapshot := reloaded.Snapshot()
	if len(snapshot.Runs) != 0 {
		t.Fatalf("expected no runs after delete, got %d", len(snapshot.Runs))
	}
}
