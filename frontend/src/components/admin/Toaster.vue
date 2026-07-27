<script setup lang="ts">
// Transient corner messages. Hand-rolled rather than pulled in: the whole toast
// surface is a list, a timer and a dismiss button, and this project does not add
// a dependency for that.
//
// Client-only by construction — the list is filled in onMounted, which SSR never
// calls — so the server emits nothing and there is no markup for the client to
// disagree with. That also means a toast never appears in the SSR snapshot tests,
// which is the correct outcome for something that dismisses itself.
import { onMounted, ref } from 'vue'
import { Check, X, TriangleAlert } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  // Keyed by kind, as the flash prop arrives: { success: "已保存" }.
  messages?: Record<string, string>
  // How long before a toast removes itself. Errors ignore this — see below.
  duration?: number
}>(), { duration: 4000 })

interface Toast {
  id: number
  kind: string
  message: string
}

const toasts = ref<Toast[]>([])
let nextID = 0

function dismiss(id: number) {
  toasts.value = toasts.value.filter((t) => t.id !== id)
}

onMounted(() => {
  for (const [kind, message] of Object.entries(props.messages ?? {})) {
    const id = nextID++
    toasts.value.push({ id, kind, message })
    // An error that dismisses itself can be missed entirely, and a missed error
    // reads as "nothing happened". Only the reassuring kinds time out.
    if (kind !== 'error') {
      setTimeout(() => dismiss(id), props.duration)
    }
  }
})
</script>

<template>
  <!-- aria-live so a screen reader announces a message that appears without any
       navigation; polite because none of these interrupt a task. -->
  <div
    v-if="toasts.length"
    class="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 flex-col gap-2"
    role="status"
    aria-live="polite"
  >
    <div
      v-for="t in toasts"
      :key="t.id"
      class="pointer-events-auto flex items-start gap-2 rounded-md border bg-card p-3 text-sm shadow-lg"
      :class="t.kind === 'error' ? 'border-destructive/40' : ''"
    >
      <component
        :is="t.kind === 'error' ? TriangleAlert : Check"
        class="mt-0.5 size-4 shrink-0"
        :class="t.kind === 'error' ? 'text-destructive' : 'text-muted-foreground'"
      />
      <span class="flex-1">{{ t.message }}</span>
      <button
        type="button"
        class="shrink-0 rounded-sm text-muted-foreground hover:text-foreground"
        aria-label="关闭"
        @click="dismiss(t.id)"
      >
        <X class="size-4" />
      </button>
    </div>
  </div>
</template>
