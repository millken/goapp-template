<script setup lang="ts">
// Every admin page opens with one: title + optional description left, actions
// right. The breadcrumb answers "where am I"; this answers "what is this page".
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="mb-6 flex items-start justify-between gap-4">
    <div>
      <h1 class="text-2xl font-semibold tracking-tight">{{ title }}</h1>
      <p v-if="description" class="mt-1 text-sm text-muted-foreground">{{ description }}</p>
    </div>
    <div class="flex shrink-0 gap-2">
      <slot name="actions" />
    </div>
  </div>
</template>
