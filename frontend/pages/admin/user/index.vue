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
type UserRow = {
  id: number
  username: string
  group_id: number
  group: string
  status: number
  created_at: number
}

defineProps<{
  items: UserRow[]
  basePath: string
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()

const columns = [
  { key: 'username', label: '用户名', sortable: true },
  { key: 'group', label: '分组' },
  { key: 'status', label: '状态' },
]

// The row awaiting delete confirmation; null closes the dialog. Overlays must
// start closed — SSR does not emit teleported content.
const pending = ref<UserRow | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as UserRow
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
    <PageHeader title="用户" description="后台账号及其权限分组。">
      <template #actions>
        <Button as="a" :href="`${basePath}/new`" size="sm">
          <Plus />
          新建用户
        </Button>
      </template>
    </PageHeader>

    <Card>
      <CardContent class="pt-6">
        <DataTable :columns="columns" :data="items" search-key="username">
          <template #cell-username="{ row }">
            <a :href="`${basePath}/${row.id}/edit`" class="font-medium hover:underline">
              {{ row.username }}
            </a>
          </template>
          <template #cell-status="{ row }">
            <Badge :variant="row.status === 1 ? 'default' : 'secondary'">
              {{ row.status === 1 ? '启用' : '已禁用' }}
            </Badge>
          </template>
          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}/edit`">编辑</DropdownMenuItem>
            <!-- w-full because a <button> is shrink-to-fit even as a flex
                 container, so without it the hover highlight stops at the text
                 instead of filling the row. -->
            <DropdownMenuItem as="button" type="submit" class="w-full" :form="`status-${row.id}`">
              {{ row.status === 1 ? '禁用' : '启用' }}
            </DropdownMenuItem>
            <DropdownMenuItem variant="destructive" @select="askDelete(row)">删除</DropdownMenuItem>
          </template>
          <template #empty>还没有用户。</template>
        </DataTable>

        <!-- One form per row, outside the dropdown: a menu item cannot carry a
             POST body itself, and a form nested inside the teleported dropdown
             content would not survive SSR. -->
        <form
          v-for="row in items"
          :id="`status-${row.id}`"
          :key="row.id"
          :action="`${basePath}/${row.id}/status`"
          method="post"
          class="hidden"
        >
          <input type="hidden" name="status" :value="row.status === 1 ? 0 : 1">
        </form>
      </CardContent>
    </Card>

    <ConfirmDialog
      :open="pending !== null"
      :title="`删除用户“${pending?.username}”？`"
      description="该账号将被永久删除，其会话会在下一次请求时失效。此操作不可撤销。"
      :action="`${basePath}/${pending?.id}/delete`"
      confirm-label="删除"
      cancel-label="取消"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
