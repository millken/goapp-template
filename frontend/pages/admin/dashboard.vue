<script setup lang="ts">
import AdminLayout from '@/components/AdminLayout.vue'

interface MenuItem { title: string; path: string; order?: number }

// Shared props injected by the admin auth middleware on authenticated requests.
defineProps<{
  adminMenu?: MenuItem[]
  adminUser?: unknown
  adminMount?: string
  loginPath?: string
}>()
</script>

<template>
  <AdminLayout :menu="adminMenu" :user="adminUser" :mount="adminMount" :login-path="loginPath">
    <h1 class="text-2xl font-bold text-gray-900 mb-2">Dashboard</h1>
    <p class="text-gray-500">Signed in as {{ adminUser }}.</p>
    <p class="text-gray-400 mt-4 text-sm">
      Generate an admin resource with <code>goapp gen admin &lt;name&gt;</code>; it registers
      itself in the menu on the left.
    </p>
  </AdminLayout>
</template>
