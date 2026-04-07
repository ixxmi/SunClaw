<script setup lang="ts">
const props = defineProps<{
  modelValue: string;
  sending?: boolean;
  disabled?: boolean;
  canSend?: boolean;
}>();

const emit = defineEmits<{
  (event: "update:modelValue", value: string): void;
  (event: "send"): void;
}>();

function handleKeydown(event: KeyboardEvent) {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    emit("send");
  }
}
</script>

<template>
  <div class="chat-composer">
    <textarea
      class="chat-composer-input"
      :value="props.modelValue"
      :disabled="disabled || sending"
      rows="4"
      placeholder="输入消息，Enter 发送，Shift + Enter 换行"
      @input="emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
      @keydown="handleKeydown"
    />
    <div class="chat-composer-actions">
      <small class="chat-composer-hint">最小方案：单会话、本地状态、live + mock fallback</small>
      <button class="primary-button" type="button" :disabled="disabled || sending || !canSend" @click="emit('send')">
        {{ sending ? "Sending..." : "Send" }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.chat-composer {
  display: grid;
  gap: 12px;
}

.chat-composer-input {
  width: 100%;
  min-height: 104px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--panel);
  color: var(--text);
  resize: vertical;
}

.chat-composer-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.chat-composer-hint {
  color: var(--muted-soft);
  line-height: 1.5;
}
</style>
