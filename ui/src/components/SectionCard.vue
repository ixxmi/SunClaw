<script setup lang="ts">
defineProps<{
  eyebrow: string;
  title: string;
  note?: string;
}>();
</script>

<template>
  <article class="section-card">
    <header class="section-head">
      <div>
        <p class="section-eyebrow">{{ eyebrow }}</p>
        <h3>{{ title }}</h3>
      </div>
      <p v-if="note" class="section-note">{{ note }}</p>
    </header>
    <div class="section-body">
      <slot />
    </div>
  </article>
</template>
