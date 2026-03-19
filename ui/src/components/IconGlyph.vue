<script setup lang="ts">
import { computed } from "vue";
import type { IconName } from "../data/dashboard";

const props = withDefaults(
  defineProps<{
    name: IconName;
    size?: number;
  }>(),
  {
    size: 18,
  },
);

const viewBox = "0 0 24 24";

const paths = computed(() => {
  switch (props.name) {
    case "menu":
      return ["M4 7h16", "M4 12h16", "M4 17h16"];
    case "message-square":
      return ["M6 5h12a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H9l-4 3v-3H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2Z"];
    case "bar-chart-2":
      return ["M6 18V9", "M12 18V5", "M18 18v-7"];
    case "link-2":
      return ["M10 14 8 16a3 3 0 1 1-4-4l2-2a3 3 0 0 1 4 0", "m14 10 2-2a3 3 0 1 1 4 4l-2 2a3 3 0 0 1-4 0", "M8 12h8"];
    case "radio":
      return ["M12 12h.01", "M16.24 7.76a6 6 0 1 1-8.48 0", "M18.36 5.64a9 9 0 1 1-12.72 0"];
    case "file-text":
      return ["M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8l-5-5Z", "M14 3v5h5", "M9 13h6", "M9 17h6"];
    case "settings":
      return ["M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z", "M19.4 15a1 1 0 0 0 .2 1.1l.1.1a1 1 0 1 1-1.4 1.4l-.1-.1a1 1 0 0 0-1.1-.2 1 1 0 0 0-.6.9V20a1 1 0 1 1-2 0v-.2a1 1 0 0 0-.7-1 1 1 0 0 0-1.1.2l-.1.1a1 1 0 1 1-1.4-1.4l.1-.1a1 1 0 0 0 .2-1.1 1 1 0 0 0-.9-.6H4a1 1 0 1 1 0-2h.2a1 1 0 0 0 1-.7 1 1 0 0 0-.2-1.1l-.1-.1a1 1 0 1 1 1.4-1.4l.1.1a1 1 0 0 0 1.1.2H8a1 1 0 0 0 .6-.9V4a1 1 0 1 1 2 0v.2a1 1 0 0 0 .7 1 1 1 0 0 0 1.1-.2l.1-.1a1 1 0 1 1 1.4 1.4l-.1.1a1 1 0 0 0-.2 1.1V8c0 .4.2.8.6.9h.2a1 1 0 0 0 1-.2l.1-.1a1 1 0 1 1 1.4 1.4l-.1.1a1 1 0 0 0-.2 1.1V12c0 .4.2.8.6.9H20a1 1 0 1 1 0 2h-.2a1 1 0 0 0-.4.1Z"];
    case "zap":
      return ["M13 2 4 14h6l-1 8 9-12h-6l1-8Z"];
    case "monitor":
      return ["M4 5h16v10H4z", "M8 19h8", "M12 15v4"];
    case "bug":
      return ["M9 9h6v8a3 3 0 1 1-6 0V9Z", "M10 5h4", "M12 5V3", "M7 10H4", "M20 10h-3", "M7 14H4", "M20 14h-3", "M8 7 6 5", "M16 7l2-2"];
    case "book-open":
      return ["M3 6.5A2.5 2.5 0 0 1 5.5 4H20v16H5.5A2.5 2.5 0 0 0 3 22z", "M20 4v16", "M8 8h6"];
    case "bot":
      return ["M9 7V4h6v3", "M8 7h8a3 3 0 0 1 3 3v5a3 3 0 0 1-3 3H8a3 3 0 0 1-3-3v-5a3 3 0 0 1 3-3Z", "M9 12h.01", "M15 12h.01", "M9 16h6"];
  }
});
</script>

<template>
  <svg
    :width="size"
    :height="size"
    :viewBox="viewBox"
    fill="none"
    xmlns="http://www.w3.org/2000/svg"
    class="icon-glyph"
  >
    <path
      v-for="path in paths"
      :key="path"
      :d="path"
      stroke="currentColor"
      stroke-width="1.8"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
</template>
