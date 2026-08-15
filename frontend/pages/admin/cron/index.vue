<script setup lang="ts">
// The cron plan list.
//
// Plans are a handful of rows and always will be — they come from code registrations —
// so this uses DataTable, unlike the task list. The one thing to notice is what the page
// cannot do: there is no "new plan" button, because a plan comes from a code
// registration and the server has no route to create one.
import { ref } from 'vue'
import { RotateCcw } from 'lucide-vue-next'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import CsrfField from '@/components/admin/CsrfField.vue'
import DataTable from '@/components/admin/DataTable.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import StatusBadge from '@/components/admin/StatusBadge.vue'
import { scheduleStatusMeta, taskStatusMeta } from '@/lib/task-status'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'

interface MenuItem { title: string; path: string; order?: number; section?: string }

type CronRow = {
  id: number
  name: string
  kind: string
  spec: string
  code_spec: string
  enabled: boolean
  present: boolean
  drifted: boolean
  known_kind: boolean
  next_run_at: number
  next_run_at_text: string
  last_fire_at: number
  last_fire_at_text: string
  last_task_id: number
  last_status: string
}

const props = defineProps<{
  items: CronRow[]
  basePath: string
  taskBasePath: string
  // Whether the caller may read the task list. cron.modify does not imply task.access,
  // so linking unconditionally would send some operators to a 403. Set by resolve
  // alongside canBrowseFiles, not derived from the sidebar.
  canViewTasks?: boolean
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string; avatar?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
  csrfToken?: string
  urlPrefix?: string
}>()

const columns = [
  { key: 'name', label: '名称', sortable: true },
  { key: 'spec', label: '表达式' },
  { key: 'enabled', label: '状态' },
  { key: 'last_fire_at', label: '上次执行' },
  { key: 'next_run_at', label: '下次执行' },
  { key: 'last_status', label: '最近结果' },
]

// Takes the loose row DataTable hands a slot: reading the three booleans out of it
// is all scheduleStatusMeta needs, and it saves a cast at the call site.
const statusOf = (row: Record<string, unknown>) =>
  scheduleStatusMeta({
    enabled: Boolean(row.enabled),
    present: Boolean(row.present),
    known_kind: Boolean(row.known_kind),
  })

// A plan the code has dropped keeps its row so an operator's expression edit and its task
// history survive a deployment that happened to be missing a handler. It is the only kind
// that can be deleted — the server refuses any other, since a live plan would be synced
// straight back on the next start.
const removed = (row: CronRow) => !row.present

const pending = ref<CronRow | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as CronRow
}
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    :csrf-token="csrfToken"
    :url-prefix="urlPrefix"
  >
    <PageHeader
      title="定时任务"
      description="计划来自代码中的注册。这里可以改表达式、暂停或手动触发一次，但不能新建。"
    />

    <Alert v-if="items.some(removed)" class="mb-4">
      <AlertDescription>
        标记为「代码中已移除」的计划不会再被触发。它们被保留下来，是为了不让一次缺少
        处理器的发布丢掉你改过的表达式和它的执行历史；确认不再需要后可以删除。
      </AlertDescription>
    </Alert>

    <Card>
      <CardContent class="pt-6">
        <DataTable :columns="columns" :data="items" search-key="name">
          <template #cell-name="{ row }">
            <a :href="`${basePath}/${row.id}/edit`" class="font-medium hover:underline">
              {{ row.name }}
            </a>
            <div class="font-mono text-xs text-muted-foreground">{{ row.kind }}</div>
          </template>

          <template #cell-spec="{ row }">
            <code class="font-mono text-xs">{{ row.spec }}</code>
            <!-- Drift is derived from spec != code_spec, so this is where an operator
                 learns their override is still in effect and what it replaced. -->
            <div v-if="row.drifted" class="text-xs text-muted-foreground">
              代码默认：<code class="font-mono">{{ row.code_spec }}</code>
            </div>
          </template>

          <template #cell-enabled="{ row }">
            <StatusBadge :meta="statusOf(row)" />
          </template>

          <template #cell-last_fire_at="{ row }">
            <span class="text-xs whitespace-nowrap text-muted-foreground">
              {{ row.last_fire_at_text || '—' }}
            </span>
          </template>

          <template #cell-next_run_at="{ row }">
            <!-- A paused or removed plan has a stored next_run_at, but showing it would
                 promise a run that is not coming. -->
            <span class="text-xs whitespace-nowrap text-muted-foreground">
              {{ row.enabled && row.present ? (row.next_run_at_text || '—') : '—' }}
            </span>
          </template>

          <template #cell-last_status="{ row }">
            <!-- Gated on last_status, not last_task_id. The status comes from a LEFT
                 JOIN, so a plan whose last task the pruner has since deleted keeps a
                 non-zero last_task_id pointing at nothing: linking it would render an
                 empty badge over a detail page that 404s. -->
            <template v-if="row.last_status">
              <!-- The badge is written once; only the link around it is conditional,
                   since cron.access does not imply task.access. -->
              <a
                v-if="canViewTasks"
                :href="`${taskBasePath}/${row.last_task_id}`"
                class="hover:underline"
              ><StatusBadge :meta="taskStatusMeta(String(row.last_status))" /></a>
              <StatusBadge v-else :meta="taskStatusMeta(String(row.last_status))" />
            </template>
            <span v-else class="text-xs text-muted-foreground">尚未执行</span>
          </template>

          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}/edit`">编辑</DropdownMenuItem>
            <!-- Kept available for a paused plan: "stop running this automatically" and
                 "never run this again" are different requests, and verifying a plan by
                 hand right after pausing it is what happens next. -->
            <DropdownMenuItem
              v-if="row.present && row.known_kind"
              as="button"
              type="submit"
              class="w-full"
              :form="`run-${row.id}`"
            >立即触发一次</DropdownMenuItem>
            <DropdownMenuItem
              v-if="row.present"
              as="button"
              type="submit"
              class="w-full"
              :form="`enabled-${row.id}`"
            >{{ row.enabled ? '暂停' : '启用' }}</DropdownMenuItem>
            <DropdownMenuItem
              v-if="row.drifted"
              as="button"
              type="submit"
              class="w-full"
              :form="`reset-${row.id}`"
            >恢复代码默认值</DropdownMenuItem>
            <DropdownMenuItem
              v-if="removed(row as unknown as CronRow)"
              variant="destructive"
              @select="askDelete(row)"
            >删除</DropdownMenuItem>
          </template>

          <template #empty>代码中还没有注册定时任务。</template>
        </DataTable>

        <!-- Hidden forms outside the dropdown, as elsewhere: a menu item cannot carry a
             POST body, and a form inside the teleported dropdown content does not survive
             SSR.

             `enabled` is sent as an explicit value rather than "toggle", so a form
             submitted twice does not flip it back. -->
        <template v-for="row in items" :key="row.id">
          <form
            v-if="row.present"
            :id="`enabled-${row.id}`"
            :action="`${basePath}/${row.id}/enabled`"
            method="post"
            class="hidden"
          >
            <CsrfField :token="csrfToken" />
            <input type="hidden" name="enabled" :value="row.enabled ? 0 : 1">
          </form>
          <form
            v-if="row.present && row.known_kind"
            :id="`run-${row.id}`"
            :action="`${basePath}/${row.id}/run`"
            method="post"
            class="hidden"
          ><CsrfField :token="csrfToken" /></form>
          <form
            v-if="row.drifted"
            :id="`reset-${row.id}`"
            :action="`${basePath}/${row.id}/reset`"
            method="post"
            class="hidden"
          ><CsrfField :token="csrfToken" /></form>
        </template>
      </CardContent>
    </Card>

    <ConfirmDialog
      :open="pending !== null"
      :title="`删除定时任务“${pending?.name}”？`"
      description="该计划及其配置将被永久删除；它已产生的任务不受影响。此操作不可撤销。"
      :action="`${basePath}/${pending?.id}/delete`"
      confirm-label="删除"
      cancel-label="取消"
      :csrf-token="csrfToken"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
