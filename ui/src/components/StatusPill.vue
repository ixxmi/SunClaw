<script setup lang="ts">
import type { Tone } from "../data/dashboard";

withDefaults(
  defineProps<{
    label: string;
    tone?: Tone;
  }>(),
  {
    tone: "slate",
  },
);
</script>

<template>
  <span class="status-pill" :class="`tone-${tone}`">{{ label }}</span>
</template>
