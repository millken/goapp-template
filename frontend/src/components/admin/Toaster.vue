<script setup lang="ts">
// Transient corner messages, on top of vue-sonner via the shadcn-vue `sonner`
// component (`goappctl gen ui sonner`). shadcn retired its own toast in favour
// of this one, so it is what `gen ui` will keep giving you.
//
// This wrapper exists so the shell keeps a single prop-shaped contract — hand it
// the flash map and it does the rest — rather than every page reaching for the
// imperative toast() API. Pages stage messages on the server; nothing in a page
// calls this directly.
//
// The toasts are raised in onMounted, which SSR never calls, so the server emits
// nothing for them. That is deliberate and pinned by a test: a message that is
// about to remove itself on a timer must not be in the markup the client
// hydrates against.
import { onMounted } from 'vue'
import { toast } from 'vue-sonner'
import { Toaster as Sonner } from '@/components/ui/sonner'

const props = withDefaults(defineProps<{
  // Keyed by kind, as the flash prop arrives: { success: "用户已创建" }.
  messages?: Record<string, string>
  duration?: number
}>(), { duration: 4000 })

onMounted(() => {
  for (const [kind, message] of Object.entries(props.messages ?? {})) {
    // An error that dismisses itself can be missed entirely, and a missed error
    // reads as "nothing happened". Only the reassuring kinds time out.
    const duration = kind === 'error' ? Number.POSITIVE_INFINITY : props.duration
    if (kind === 'error') {
      toast.error(message, { duration, closeButton: true })
    } else {
      toast.success(message, { duration })
    }
  }
})
</script>

<template>
  <Sonner position="bottom-right" rich-colors />
</template>
