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

func TestShrimpBrainPersistsNestedSubagentChain(t *testing.T) {
	dir := t.TempDir()
	tracker := NewShrimpBrainTracker(dir)

	runID := tracker.StartMainTask("msg-3", "block-nested", "user-nested", "session-main", "planner", "cli", "chat-3", "做一次多级协作")
	if runID == "" {
		t.Fatalf("expected run id")
	}

	tracker.RecordLoopNode("session-main", "planner", false, 1, "tool_calls", "先派给 frontend", 1)
	tracker.RecordSubagentDispatchAt("session-main", "session-frontend", "frontend", "frontend-polish", "优化页面", 1)

	tracker.RecordPrompt("session-frontend", "frontend", true, "frontend prompt", []PromptLayerSnapshot{
		{Name: "subagent_descriptor", Enabled: true, Source: "dynamic_subagent"},
	})
	tracker.RecordLoopNode("session-frontend", "frontend", true, 1, "tool_calls", "我再派给 qa 校验", 1)
	tracker.RecordSubagentDispatchAt("session-frontend", "session-qa", "qa", "qa-check", "校验页面", 1)

	tracker.RecordPrompt("session-qa", "qa", true, "qa prompt", []PromptLayerSnapshot{
		{Name: "subagent_descriptor", Enabled: true, Source: "dynamic_subagent"},
	})
	tracker.RecordLoopNode("session-qa", "qa", true, 1, "tool_calls", "开始校验", 1)
	tracker.RecordToolCall("session-qa", "qa", true, 1, "tool-qa-1", "check_ui", map[string]any{"path": "ui/src/App.vue"}, "checked ui", "")
	tracker.RecordSubagentResult("session-qa", "qa", "completed", "QA 已完成", "")
	tracker.RecordSubagentResult("session-frontend", "frontend", "completed", "Frontend 已完成", "")
	tracker.RecordMainReply("session-main", "planner", "多级协作完成")

	reloaded := NewShrimpBrainTracker(dir)
	snapshot := reloaded.Snapshot()
	if len(snapshot.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(snapshot.Runs))
	}

	mainLoop := snapshot.Runs[0].MainLoops[0]
	if len(mainLoop.ToolCalls) != 1 {
		t.Fatalf("expected 1 main tool call, got %d", len(mainLoop.ToolCalls))
	}

	frontendDispatch := mainLoop.ToolCalls[0]
	if len(frontendDispatch.ChildLoops) != 1 {
		t.Fatalf("expected 1 frontend child loop, got %d", len(frontendDispatch.ChildLoops))
	}

	frontendLoop := frontendDispatch.ChildLoops[0]
	if len(frontendLoop.ToolCalls) != 1 {
		t.Fatalf("expected 1 nested dispatch on frontend loop, got %d", len(frontendLoop.ToolCalls))
	}

	qaDispatch := frontendLoop.ToolCalls[0]
	if qaDispatch.ToolName != "sessions_spawn" {
		t.Fatalf("expected nested sessions_spawn, got %q", qaDispatch.ToolName)
	}
	if qaDispatch.ChildAgentID != "qa" {
		t.Fatalf("expected nested child agent qa, got %q", qaDispatch.ChildAgentID)
	}
	if qaDispatch.ChildPrompt != "qa prompt" {
		t.Fatalf("expected nested child prompt persisted, got %q", qaDispatch.ChildPrompt)
	}
	if qaDispatch.ChildReply != "QA 已完成" {
		t.Fatalf("expected nested child reply persisted, got %q", qaDispatch.ChildReply)
	}
	if len(qaDispatch.ChildLoops) != 1 {
		t.Fatalf("expected 1 qa child loop, got %d", len(qaDispatch.ChildLoops))
	}
	if len(qaDispatch.ChildLoops[0].ToolCalls) != 1 {
		t.Fatalf("expected qa tool call persisted, got %d", len(qaDispatch.ChildLoops[0].ToolCalls))
	}
	if qaDispatch.ChildLoops[0].ToolCalls[0].ToolName != "check_ui" {
		t.Fatalf("expected qa tool check_ui, got %q", qaDispatch.ChildLoops[0].ToolCalls[0].ToolName)
	}
}
