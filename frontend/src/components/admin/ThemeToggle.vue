<script setup lang="ts">
// Toggles the dark class and persists the choice. Initial state is read in
// onMounted from the class the boot script already set — nothing here touches
// document at setup time, so the component is inert under QuickJS SSR.
import { onMounted, ref } from 'vue'
import { Moon, Sun } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'

const dark = ref(false)
onMounted(() => {
  dark.value = document.documentElement.classList.contains('dark')
})
function toggle() {
  dark.value = !dark.value
  document.documentElement.classList.toggle('dark', dark.value)
  localStorage.theme = dark.value ? 'dark' : 'light'
}
</script>

<template>
  <Button variant="ghost" size="icon-sm" aria-label="切换主题" @click="toggle">
    <Sun v-if="!dark" />
    <Moon v-else />
  </Button>
</template>
