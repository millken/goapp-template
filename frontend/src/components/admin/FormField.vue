<script setup lang="ts">
// Label + control + server-side error, the repeating unit of every admin form.
// Errors come only from the server's `errors` prop — there is no client-side
// validation. The wrapper styles the slotted control when invalid, so pages
// don't repeat the border class.
import { Label } from '@/components/ui/label'

defineProps<{ name: string; label: string; error?: string }>()
</script>

<template>
  <div
    class="space-y-1.5"
    :class="error ? '[&_input]:border-destructive [&_select]:border-destructive [&_textarea]:border-destructive' : ''"
  >
    <Label :for="name">{{ label }}</Label>
    <slot />
    <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
  </div>
</template>
