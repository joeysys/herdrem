<script setup lang="ts">
// Header action icons. Unicode glyphs (🗂 ⎇ ± ✎ ⌕ ⏱ …) come from different
// fonts — an emoji here, a symbol fallback there — so their widths and
// baselines never matched. One stroke set on a fixed box keeps the buttons and
// the mobile menu's labels lined up.
export type TermIconName =
  | 'files' | 'diff' | 'status' | 'todo' | 'search' | 'history'
  | 'fullscreen' | 'zoom' | 'refresh' | 'settings' | 'more'

const PATHS: Record<TermIconName, string> = {
  files: '<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
  diff: '<circle cx="6" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="6" r="2.5"/><path d="M6 8.5v7M18 8.5c0 5-6 5-10 8"/>',
  status: '<path d="M12 4v10M7 9h10M7 19h10"/>',
  todo: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13 7l4 4"/>',
  search: '<circle cx="11" cy="11" r="6"/><path d="M20 20l-4.5-4.5"/>',
  history: '<circle cx="12" cy="12" r="8"/><path d="M12 8v4l3 2"/>',
  fullscreen: '<path d="M4 9V4h5M15 4h5v5M20 15v5h-5M9 20H4v-5"/>',
  zoom: '<path d="M14 4h6v6M10 20H4v-6M20 4l-7 7M4 20l7-7"/>',
  refresh: '<path d="M20 12a8 8 0 1 1-2.34-5.66"/><path d="M20 4v5h-5"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.93 4.93l2.12 2.12M16.95 16.95l2.12 2.12M2 12h3M19 12h3M4.93 19.07l2.12-2.12M16.95 7.05l2.12-2.12"/>',
  more: '<circle cx="12" cy="5" r="1.5" fill="currentColor"/><circle cx="12" cy="12" r="1.5" fill="currentColor"/><circle cx="12" cy="19" r="1.5" fill="currentColor"/>',
}

withDefaults(defineProps<{ name: TermIconName; size?: number }>(), { size: 16 })
</script>

<template>
  <svg
    class="term-icon"
    viewBox="0 0 24 24"
    :width="size"
    :height="size"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    focusable="false"
    v-html="PATHS[name]"
  />
</template>

<style scoped>
.term-icon { display: block; flex-shrink: 0; }
</style>
