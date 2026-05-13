<template>
  <component :is="tag" ref="linkEl">
    <slot />
  </component>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { pjaxClick } from './pjax-loader'

withDefaults(
  defineProps<{
    /** Tag to render. Defaults to <a>. PJAX only intercepts anchor clicks. */
    tag?: string
  }>(),
  { tag: 'a' },
)

defineOptions({ name: 'InertiaLink' })

const linkEl = ref<HTMLElement | null>(null)
let cleanup: { destroy(): void } | undefined

onMounted(() => {
  const el = linkEl.value
  if (el && el instanceof HTMLAnchorElement) {
    cleanup = pjaxClick(el)
  }
})

onBeforeUnmount(() => {
  cleanup?.destroy()
  cleanup = undefined
})
</script>
