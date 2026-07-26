<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

interface MenuItem { title: string; path: string; order?: number; section?: string }

// Shared props injected by the admin auth middleware on authenticated requests.
defineProps<{
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :login-path="loginPath"
    :current-path="currentPath"
    :flash="flash"
  >
    <PageHeader title="Dashboard" :description="`Signed in as ${adminUser?.username ?? ''}.`" />
    <Card>
      <CardHeader>
        <CardTitle>Getting started</CardTitle>
        <CardDescription>This project scaffolds admin resources.</CardDescription>
      </CardHeader>
      <CardContent class="text-sm text-muted-foreground">
        Generate an admin resource with <code>goappctl gen admin &lt;name&gt;</code>;
        it registers itself in the menu on the left.
      </CardContent>
    </Card>
  </AdminShell>
</template>
