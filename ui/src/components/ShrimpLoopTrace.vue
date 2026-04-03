<script setup lang="ts">
import type { ShrimpBrainLoopNode, ShrimpBrainToolCall } from "../data/dashboard";

defineOptions({
  name: "ShrimpLoopTrace",
});

const props = withDefaults(
  defineProps<{
    loop: ShrimpBrainLoopNode;
    depth?: number;
  }>(),
  {
    depth: 0,
  },
);

function statusBucket(status: string) {
  switch ((status || "").trim().toLowerCase()) {
    case "completed":
    case "success":
    case "ok":
      return "completed";
    case "running":
    case "streaming":
    case "working":
      return "running";
    case "error":
    case "failed":
    case "cancelled":
    case "timeout":
      return "failed";
    default:
      return "pending";
  }
}

function statusClass(status: string) {
  return `trace-status-badge is-${statusBucket(status)}`;
}

function isFailureStatus(status: string) {
  return statusBucket(status) === "failed";
}

function formatTimeShort(timestamp?: number) {
  if (!timestamp) {
    return "";
  }
  const d = new Date(timestamp);
  return `${d.getHours().toString().padStart(2, "0")}:${d.getMinutes().toString().padStart(2, "0")}:${d.getSeconds().toString().padStart(2, "0")}`;
}

function loopTitle(loop: ShrimpBrainLoopNode) {
  return props.depth === 0 ? `主 Agent 外循环 ${loop.iteration}` : `子 Agent 内循环 ${loop.iteration}`;
}

function toolTitle(tool: ShrimpBrainToolCall) {
  if (tool.toolName === "sessions_spawn" && tool.childAgentId) {
    return `派发给 ${tool.childAgentId}`;
  }
  return tool.toolName;
}

function toolKindLabel(tool: ShrimpBrainToolCall) {
  return tool.toolName === "sessions_spawn" ? "SYSTEM" : "TOOL";
}

function toolDisplayStatus(tool: ShrimpBrainToolCall) {
  return tool.childStatus || tool.status;
}

function hasToolPayload(tool: ShrimpBrainToolCall) {
  return Boolean(tool.arguments || tool.result || tool.error || tool.childPrompt || tool.childReply);
}

function truncateText(value?: string, limit = 80) {
  const content = value?.trim() ?? "";
  if (!content || content.length <= limit) {
    return content;
  }
  return `${content.slice(0, limit - 1)}…`;
}

function loopSummary(loop: ShrimpBrainLoopNode) {
  const content = loop.summary?.trim() ?? "";
  if (!content) {
    return "";
  }
  return content.replace(/^Loop\s+\d+\s*·\s*/i, "");
}
</script>

<template>
  <details class="trace-node" :class="{ failed: isFailureStatus(loop.status), nested: depth > 0 }" open>
    <summary class="trace-node-summary">
      <span class="trace-head">
        <span class="trace-head-main">
          <strong>{{ loopTitle(loop) }}</strong>
          <span class="trace-head-sub">{{ loop.agentId || loop.id }}</span>
        </span>
        <span :class="statusClass(loop.status)">{{ loop.status }}</span>
      </span>

      <span class="trace-meta">
        <span>session: {{ loop.sessionKey }}</span>
        <span>迭代: {{ loop.iteration }}</span>
        <span v-if="loop.stopReason">stop: {{ loop.stopReason }}</span>
        <span>更新: {{ formatTimeShort(loop.updatedAt) }}</span>
      </span>

      <span v-if="loopSummary(loop) && !loop.toolCalls?.length" class="muted trace-summary">{{ loopSummary(loop) }}</span>
    </summary>

    <details v-if="loop.reply" class="trace-details">
      <summary>本轮回复</summary>
      <pre>{{ loop.reply }}</pre>
    </details>

    <details v-if="loop.toolCalls?.length" class="trace-details trace-tool-group">
      <summary>
        <span>工具调用</span>
        <small>{{ loopSummary(loop) || `${loop.toolCalls.length} tools` }}</small>
      </summary>

      <div class="trace-tool-list">
        <details
          v-for="tool in loop.toolCalls"
          :key="tool.id || `${loop.id}-${tool.toolName}-${tool.updatedAt}`"
          class="trace-tool-card"
          :class="{ failed: isFailureStatus(toolDisplayStatus(tool)) }"
        >
          <summary class="trace-tool-summary">
            <span class="trace-head">
              <span class="trace-head-main">
                <span class="trace-tool-topline">
                  <span class="trace-tool-kind">{{ toolKindLabel(tool) }}</span>
                  <strong>{{ toolTitle(tool) }}</strong>
                </span>
                <span v-if="tool.summary" class="trace-head-sub">{{ tool.summary }}</span>
                <span v-else-if="tool.task" class="trace-head-sub">{{ truncateText(tool.task, 120) }}</span>
              </span>
              <span :class="statusClass(toolDisplayStatus(tool))">{{ toolDisplayStatus(tool) }}</span>
            </span>
          </summary>

          <div class="trace-tool-body">
            <div class="trace-meta">
              <span v-if="tool.label">{{ tool.label }}</span>
              <span v-if="tool.task">task: {{ truncateText(tool.task, 60) }}</span>
              <span v-if="tool.childAgentId">child: {{ tool.childAgentId }}</span>
              <span v-if="tool.childSessionKey">session: {{ tool.childSessionKey }}</span>
              <span>更新: {{ formatTimeShort(tool.updatedAt) }}</span>
            </div>

            <details v-if="tool.arguments" class="trace-details trace-payload">
              <summary>参数</summary>
              <pre>{{ tool.arguments }}</pre>
            </details>

            <details v-if="tool.result" class="trace-details trace-payload">
              <summary>结果</summary>
              <pre>{{ tool.result }}</pre>
            </details>

            <details v-if="tool.error" class="trace-details trace-payload danger">
              <summary>错误</summary>
              <pre>{{ tool.error }}</pre>
            </details>

            <details v-if="tool.childPrompt || tool.childReply || tool.childLoops?.length" class="trace-subagent-branch">
              <summary>
                <span>子 Agent</span>
                <small>{{ tool.childAgentId || "unknown" }}</small>
                <small v-if="tool.childSessionKey">session: {{ tool.childSessionKey }}</small>
                <small v-if="tool.childStatus">{{ tool.childStatus }}</small>
              </summary>

              <div v-if="tool.childPromptLayers?.length" class="trace-chip-row">
                <span v-for="(layer, index) in tool.childPromptLayers" :key="`${tool.id}-layer-${index}`">{{ layer }}</span>
              </div>

              <div v-if="tool.childPromptSources?.length" class="trace-chip-row">
                <span v-for="(source, index) in tool.childPromptSources" :key="`${tool.id}-source-${index}`">{{ source }}</span>
              </div>

              <details v-if="tool.childPrompt" class="trace-details trace-payload">
                <summary>子 Agent 提示词</summary>
                <pre>{{ tool.childPrompt }}</pre>
              </details>

              <details v-if="tool.childReply" class="trace-details trace-payload">
                <summary>子 Agent 回复</summary>
                <pre>{{ tool.childReply }}</pre>
              </details>

              <div v-if="tool.childLoops?.length" class="trace-children">
                <ShrimpLoopTrace
                  v-for="childLoop in tool.childLoops"
                  :key="childLoop.id"
                  :loop="childLoop"
                  :depth="depth + 1"
                />
              </div>
            </details>

            <p v-else-if="!tool.summary && !hasToolPayload(tool)" class="muted trace-summary">暂无更多调用详情</p>
          </div>
        </details>
      </div>
    </details>

    <p v-else-if="!loop.reply" class="muted trace-summary">暂无本轮详情</p>
  </details>
</template>

<style scoped>
.trace-node,
.trace-details,
.trace-subagent-branch {
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--panel);
}

.trace-node {
  padding: 0;
  overflow: hidden;
}

.trace-node[open] {
  padding-bottom: 10px;
}

.trace-node-summary {
  display: grid;
  gap: 6px;
  padding: 10px 12px;
  cursor: pointer;
  list-style: none;
}

.trace-node-summary::-webkit-details-marker {
  display: none;
}

.trace-node.nested {
  background: linear-gradient(180deg, rgba(37, 99, 235, 0.05), rgba(37, 99, 235, 0.02));
  border-color: rgba(37, 99, 235, 0.18);
  box-shadow: inset 0 0 0 1px rgba(37, 99, 235, 0.04);
}

.trace-node.failed,
.trace-tool-card.failed,
.trace-details.danger {
  border-color: #fecaca;
  background: #fff7f7;
}

.trace-tool-card {
  border: 1px solid var(--line);
  border-left: 3px solid rgba(37, 99, 235, 0.22);
  border-radius: 10px;
  background: linear-gradient(180deg, rgba(37, 99, 235, 0.045), rgba(37, 99, 235, 0.015));
  padding: 0;
  overflow: hidden;
}

.trace-tool-card[open] {
  padding-bottom: 8px;
}

.trace-tool-card.failed {
  border-left-color: #fca5a5;
}

.trace-tool-summary {
  display: grid;
  gap: 4px;
  padding: 8px 10px;
  cursor: pointer;
  list-style: none;
}

.trace-tool-summary::-webkit-details-marker {
  display: none;
}

.trace-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.trace-head > div,
.trace-head-main {
  min-width: 0;
}

.trace-head strong {
  display: block;
  font-size: 12px;
}

.trace-head-main {
  display: grid;
  gap: 2px;
}

.trace-head p {
  margin: 3px 0 0;
  color: var(--muted);
  font-size: 11px;
  line-height: 1.45;
}

.trace-head-sub {
  color: var(--muted);
  font-size: 11px;
  line-height: 1.45;
}

.trace-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 8px;
  color: var(--muted-soft);
  font-size: 10px;
  font-variant-numeric: tabular-nums;
}

.trace-summary {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
}

.trace-tool-list,
.trace-children {
  display: grid;
  gap: 6px;
}

.trace-children {
  margin-top: 6px;
  margin-left: 8px;
  padding-left: 14px;
  border-left: 3px solid rgba(37, 99, 235, 0.18);
}

.trace-tool-body {
  display: grid;
  gap: 6px;
  padding: 0 10px;
}

.trace-details {
  padding: 0;
}

.trace-details[open] {
  padding-bottom: 10px;
}

.trace-details summary,
.trace-tool-group summary,
.trace-subagent-branch summary {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 28px;
  padding: 0 10px;
  cursor: pointer;
  list-style: none;
  color: var(--text);
  font-size: 11px;
  font-weight: 600;
}

.trace-details summary::-webkit-details-marker,
.trace-tool-group summary::-webkit-details-marker,
.trace-subagent-branch summary::-webkit-details-marker {
  display: none;
}

.trace-tool-group summary small,
.trace-subagent-branch summary small {
  color: var(--muted);
  font-size: 10px;
  font-weight: 600;
}

.trace-tool-group summary small {
  margin-left: auto;
}

.trace-subagent-branch {
  padding: 0;
  background: var(--panel-soft);
}

.trace-subagent-branch[open] {
  padding-bottom: 10px;
}

.trace-subagent-branch summary {
  min-height: 30px;
}

.trace-subagent-branch summary small:last-child {
  margin-left: auto;
}

.trace-details.trace-payload {
  background: rgba(148, 163, 184, 0.08);
  border-color: rgba(148, 163, 184, 0.2);
}

.trace-details pre {
  margin: 8px 10px 0;
  overflow-x: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
  line-height: 1.6;
  font-size: 12px;
  color: var(--text);
}

.trace-details.trace-payload pre {
  margin: 6px 10px 0;
  padding: 9px 10px;
  border: 1px solid rgba(148, 163, 184, 0.18);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.72);
  line-height: 1.55;
  font-size: 11.5px;
}

.trace-chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin: 4px 10px 0;
}

.trace-chip-row span {
  padding: 2px 6px;
  border-radius: 999px;
  background: var(--slate-soft);
  color: var(--slate);
  font-size: 10px;
}

.trace-tool-topline {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.trace-tool-kind {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 16px;
  padding: 0 6px;
  border-radius: 999px;
  background: rgba(37, 99, 235, 0.1);
  color: var(--accent-strong);
  font-size: 9px;
  font-weight: 800;
  letter-spacing: 0.06em;
}

.trace-status-badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 18px;
  padding: 0 7px;
  border-radius: 999px;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  white-space: nowrap;
}

.trace-status-badge.is-completed {
  color: var(--emerald);
  background: var(--emerald-soft);
}

.trace-status-badge.is-running {
  color: var(--sky);
  background: var(--sky-soft);
}

.trace-status-badge.is-failed {
  color: #dc2626;
  background: #fee2e2;
}

.trace-status-badge.is-pending {
  color: var(--slate);
  background: var(--slate-soft);
}

@media (max-width: 760px) {
  .trace-head {
    flex-direction: column;
  }

  .trace-children {
    padding-left: 8px;
  }
}
</style>
