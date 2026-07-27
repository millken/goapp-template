<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import FileManager from '@/components/admin/FileManager.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Card, CardContent } from '@/components/ui/card'

interface MenuItem { title: string; path: string; order?: number; section?: string }

defineProps<{
  basePath: string
  urlPrefix: string
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
  csrfToken?: string
}>()
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    :csrf-token="csrfToken"
  >
    <PageHeader title="文件" />
    <Card>
      <CardContent class="pt-6">
        <FileManager :base-path="basePath" :url-prefix="urlPrefix" :csrf-token="csrfToken" />
      </CardContent>
    </Card>
  </AdminShell>
</template>
