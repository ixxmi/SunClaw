<script setup lang="ts">
import type { ChatMessage } from "../api/chat";

defineProps<{
  message: ChatMessage;
}>();

function formatTime(timestamp: number) {
  return new Date(timestamp).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
  });
}

function roleLabel(role: ChatMessage["role"]) {
  switch (role) {
    case "user":
      return "You";
    case "assistant":
      return "SunClaw";
    default:
      return "System";
  }
}
</script>

<template>
  <article class="chat-message-item" :class="`is-${message.role}`">
    <div class="chat-message-meta">
      <strong>{{ roleLabel(message.role) }}</strong>
      <span>{{ formatTime(message.createdAt) }}</span>
      <small v-if="message.mode" class="chat-message-mode">{{ message.mode }}</small>
    </div>
    <p class="chat-message-content">{{ message.content }}</p>
  </article>
</template>

<style scoped>
.chat-message-item {
  display: grid;
  gap: 8px;
  max-width: min(720px, 100%);
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--panel);
}

.chat-message-item.is-user {
  margin-left: auto;
  background: var(--accent-soft);
  border-color: rgba(37, 99, 235, 0.18);
}

.chat-message-item.is-assistant {
  margin-right: auto;
}

.chat-message-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  color: var(--muted);
  font-size: 12px;
}

.chat-message-mode {
  padding: 1px 8px;
  border-radius: 999px;
  background: var(--slate-soft);
  color: var(--slate);
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
}

.chat-message-content {
  margin: 0;
  color: var(--text);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
