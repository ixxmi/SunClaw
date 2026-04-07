<script setup lang="ts">
import type { DashboardQuickAction } from "../data/dashboard";

defineProps<{
  items: DashboardQuickAction[];
}>();

const emit = defineEmits<{
  (event: "select", value: string): void;
}>();
</script>

<template>
  <div class="chat-quick-actions">
    <button
      v-for="item in items"
      :key="item.label"
      class="chat-quick-action"
      type="button"
      @click="emit('select', item.label)"
    >
      <strong>{{ item.label }}</strong>
      <p>{{ item.note }}</p>
    </button>
  </div>
</template>

<style scoped>
.chat-quick-actions {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}

.chat-quick-action {
  display: grid;
  gap: 8px;
  padding: 14px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--panel);
  text-align: left;
  color: var(--text);
  cursor: pointer;
  transition:
    transform 160ms ease,
    border-color 160ms ease,
    box-shadow 160ms ease;
}

.chat-quick-action:hover {
  transform: translateY(-1px);
  border-color: rgba(96, 165, 250, 0.32);
  box-shadow: 0 10px 22px rgba(15, 23, 42, 0.08);
}

.chat-quick-action strong,
.chat-quick-action p {
  margin: 0;
}

.chat-quick-action p {
  color: var(--muted);
  line-height: 1.5;
}
</style>
