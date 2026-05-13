<script setup lang="ts">
defineProps<{
  message: string
}>()
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-gray-50">
    <div class="text-center">
      <h1 class="text-4xl font-bold text-gray-900 mb-4">{{ message }}</h1>
      <p class="text-gray-500">Edit <code>pages/Home.vue</code> to get started.</p>
    </div>
  </div>
</template>
