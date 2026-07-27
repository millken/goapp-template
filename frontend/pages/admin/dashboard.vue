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
    :current-path="currentPath"
    :flash="flash"
  >
    <PageHeader title="仪表盘" :description="`已登录为 ${adminUser?.username ?? ''}。`" />
    <Card>
      <CardHeader>
        <CardTitle>从这里开始</CardTitle>
        <CardDescription>这个项目可以脚手架生成后台资源。</CardDescription>
      </CardHeader>
      <CardContent class="text-sm text-muted-foreground">
        用 <code>goappctl gen admin &lt;name&gt;</code> 生成一个后台资源，
        它会把自己注册进左边的菜单。
      </CardContent>
    </Card>
  </AdminShell>
</template>
