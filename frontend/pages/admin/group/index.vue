<script setup lang="ts">
import { ref } from 'vue'
import { Plus } from 'lucide-vue-next'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import DataTable from '@/components/admin/DataTable.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'

interface MenuItem { title: string; path: string; order?: number; section?: string }
type GroupRow = { id: number; name: string; superuser: boolean; members: number; keys: number }

defineProps<{
  items: GroupRow[]
  basePath: string
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()

const columns = [
  { key: 'name', label: '分组', sortable: true },
  { key: 'superuser', label: '权限' },
  { key: 'members', label: '成员' },
]

// The row awaiting delete confirmation; null closes the dialog. Overlays must
// start closed — SSR does not emit teleported content.
const pending = ref<GroupRow | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as GroupRow
}
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
  >
    <PageHeader title="分组" description="权限挂在分组上，不挂在个人上。">
      <template #actions>
        <Button as="a" :href="`${basePath}/new`" size="sm">
          <Plus />
          新建分组
        </Button>
      </template>
    </PageHeader>

    <Card>
      <CardContent class="pt-6">
        <DataTable :columns="columns" :data="items" search-key="name">
          <template #cell-name="{ row }">
            <a :href="`${basePath}/${row.id}/edit`" class="font-medium hover:underline">
              {{ row.name }}
            </a>
          </template>
          <template #cell-superuser="{ row }">
            <Badge v-if="row.superuser" variant="secondary">超级管理员</Badge>
            <span v-else class="text-muted-foreground">{{ row.keys }} 项权限</span>
          </template>
          <template #cell-members="{ row }">
            <span class="tabular-nums">{{ row.members }}</span>
          </template>
          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}/edit`">编辑</DropdownMenuItem>
            <DropdownMenuItem variant="destructive" @select="askDelete(row)">删除</DropdownMenuItem>
          </template>
          <template #empty>还没有分组。</template>
        </DataTable>
      </CardContent>
    </Card>

    <ConfirmDialog
      :open="pending !== null"
      :title="`删除分组“${pending?.name}”？`"
      description="只有没有成员的分组可以删除。此操作不可撤销。"
      :action="`${basePath}/${pending?.id}/delete`"
      confirm-label="删除"
      cancel-label="取消"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
