<script setup lang="ts">
import AdminLayout from '@/components/AdminLayout.vue'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

interface MenuItem { title: string; path: string; order?: number }

// Shared props injected by the admin auth middleware on authenticated requests.
defineProps<{
  adminMenu?: MenuItem[]
  adminUser?: unknown
  adminMount?: string
  loginPath?: string
  flash?: Record<string, string>
}>()
</script>

<template>
  <AdminLayout
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :login-path="loginPath"
    :flash="flash"
  >
    <Card>
      <CardHeader>
        <CardTitle>Dashboard</CardTitle>
        <CardDescription>Signed in as {{ adminUser }}.</CardDescription>
      </CardHeader>
      <CardContent class="text-sm text-muted-foreground">
        Generate an admin resource with <code>goappctl gen admin &lt;name&gt;</code>;
        it registers itself in the menu on the left.
      </CardContent>
    </Card>
  </AdminLayout>
</template>
