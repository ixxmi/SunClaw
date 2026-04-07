<script setup lang="ts">
import ChatMessageItem from "./ChatMessageItem.vue";
import type { ChatMessage } from "../api/chat";

defineProps<{
  messages: ChatMessage[];
  sending?: boolean;
}>();
</script>

<template>
  <div class="chat-message-list">
    <ChatMessageItem
      v-for="message in messages"
      :key="message.id"
      :message="message"
    />
    <div v-if="sending" class="chat-message-pending">
      <span></span>
      <span></span>
      <span></span>
    </div>
  </div>
</template>

<style scoped>
.chat-message-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.chat-message-pending {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 10px 12px;
  border: 1px solid var(--line);
  border-radius: 999px;
  background: var(--panel);
  width: fit-content;
}

.chat-message-pending span {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: var(--muted-soft);
  animation: chat-bounce 1s infinite ease-in-out;
}

.chat-message-pending span:nth-child(2) {
  animation-delay: 0.15s;
}

.chat-message-pending span:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes chat-bounce {
  0%,
  80%,
  100% {
    transform: scale(0.7);
    opacity: 0.5;
  }
  40% {
    transform: scale(1);
    opacity: 1;
  }
}
</style>
