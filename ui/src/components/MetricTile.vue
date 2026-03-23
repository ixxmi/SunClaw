<script setup lang="ts">
import type { DashboardCard } from "../data/dashboard";
import StatusPill from "./StatusPill.vue";

defineProps<{
  metric: DashboardCard;
}>();
</script>

<template>
  <article class="metric-tile">
    <div class="metric-topline">
      <span>{{ metric.label }}</span>
      <StatusPill :label="metric.value" :tone="metric.tone" />
    </div>
    <strong>{{ metric.value }}</strong>
    <p>{{ metric.note }}</p>
  </article>
</template>
