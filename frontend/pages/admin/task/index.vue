<script setup lang="ts">
// The task list.
//
// Server-side filtered and paged, and it carries no network code at all: the filter is
// a plain <form method="get"> and the pager is anchors, both of which PJAX turns into
// navigations. That also makes a filtered list a shareable URL, which is the opposite
// trade the file manager makes — it uses fetch precisely so browsing directories does
// not fill up the back button.
//
// It uses ServerTable rather than DataTable. Not a preference: DataTable's footer counts
// `data.length`, so handed one page of a large table it would report the page size as the
// total. See the comment at the top of ServerTable.vue.
//
// No auto-refresh. A "刷新" link is one anchor and PJAX replaces the same URL in place;
// polling would need a new public API on the PJAX layer (navigate is not exported), and
// it would replace the DOM under an operator who has an error stack expanded. If it is
// ever wanted, the detail page is the place for it — a single primary-key read.
import { computed, ref } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import CsrfField from '@/components/admin/CsrfField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import ServerTable from '@/components/admin/ServerTable.vue'
import StatusBadge from '@/components/admin/StatusBadge.vue'
import { TASK_STATUSES, taskActions, taskStatusMeta } from '@/lib/task-status'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'

interface MenuItem { title: string; path: string; order?: number; section?: string }

// A type alias, not an interface: ServerTable's `data` prop is
// Record<string, unknown>[], and only an alias satisfies that index signature
// structurally.
type TaskRow = {
  id: number
  kind: string
  status: string
  attempts: number
  max_attempts: number
  run_at: number
  run_at_text: string
  created_at: number
  created_at_text: string
  finished_at: number
  finished_at_text: string
  last_error: string
  schedule_id: number
  known_kind: boolean
}

const props = defineProps<{
  items: TaskRow[]
  total: number
  page: number
  pageSize: number
  filters: { status: string; kind: string }
  kinds: string[]
  counts: Record<string, number>
  orphans: Record<string, number>
  basePath: string
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
  { key: 'id', label: '#', class: 'w-16' },
  { key: 'kind', label: '类型' },
  { key: 'status', label: '状态', class: 'w-44' },
  { key: 'run_at', label: '计划执行', class: 'w-40 whitespace-nowrap' },
  { key: 'last_error', label: '最近错误' },
]

// Query strings are built with encodeURIComponent rather than URLSearchParams.
//
// Not a style choice: URLSearchParams does not exist in QuickJS, which is the SSR
// runtime. Using it renders fine under Node and throws under SSR — and because Vue
// reports the throw through console.error, which QuickJS also lacks, the failure
// arrives as "console is not defined" with no mention of the real cause.
// lib/media-url.ts hand-rolls escaping for the same reason.
const queryString = (pairs: [string, string][]) =>
  pairs
    .filter(([, v]) => v !== '')
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join('&')

// The filter values that have to survive a page click or an action. Built here rather
// than in ServerTable because the page is what knows which parameters matter.
const filterPairs = computed<[string, string][]>(() => [
  ['status', props.filters.status ?? ''],
  ['kind', props.filters.kind ?? ''],
])

const pageHref = (page: number) => {
  const s = queryString([...filterPairs.value, ['page', page > 1 ? String(page) : '']])
  return s ? `${props.basePath}?${s}` : props.basePath
}

// Appended to every action's form action, so the handler can rebuild where the operator
// was. The handler validates each key against its own whitelist — nothing here is
// trusted as a redirect target.
const returnQuery = computed(() => {
  const s = queryString([...filterPairs.value, ['page', props.page > 1 ? String(props.page) : '']])
  return s ? `?${s}` : ''
})

const filtered = computed(() => Boolean(props.filters.status || props.filters.kind))
const refreshHref = computed(() => pageHref(props.page))

// Both take the loose row ServerTable hands a slot rather than a TaskRow: the shared
// helpers only read primitives out of it, so going through String()/Number() here is
// what lets the template drop the `row as unknown as TaskRow` cast at every call.
// The filter's vocabulary, built from the same table the badges use rather than sent
// as a prop. It was a prop, on the reasoning that a shared source would mean shipping
// the map to the browser — but the badge in every row already imports it, so the map
// was in the bundle either way and the labels were simply maintained twice.
const statuses = TASK_STATUSES.map((value) => ({ value, label: taskStatusMeta(value).label }))

const actionsFor = (row: Record<string, unknown>) => taskActions(String(row.status))
const statusMeta = (row: Record<string, unknown>) =>
  taskStatusMeta(String(row.status), Number(row.attempts), Number(row.max_attempts))

// The row awaiting delete confirmation; null closes the dialog. Overlays must start
// closed — SSR does not emit teleported content, so an open one would appear out of
// nowhere at hydration.
const pending = ref<TaskRow | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as TaskRow
}

const orphanKinds = computed(() => Object.entries(props.orphans))
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
    <PageHeader title="任务" description="异步任务的执行情况与失败日志。">
      <template #actions>
        <Button as="a" :href="refreshHref" variant="outline" size="sm">
          <RefreshCw />
          刷新
        </Button>
      </template>
    </PageHeader>

    <!-- A task whose kind has no handler in this process is never claimed, so it sits
         still with no visible reason. Saying so is the difference between "the queue is
         stuck" and "this kind was renamed or its worker is not deployed". -->
    <Alert v-if="orphanKinds.length" variant="destructive" class="mb-4">
      <AlertDescription>
        以下类型的待执行任务在当前进程中没有对应的处理器，不会被执行：
        <span v-for="([kind, n], i) in orphanKinds" :key="kind">
          <template v-if="i > 0">、</template>
          <code class="font-mono">{{ kind }}</code>（{{ n }}）
        </span>
      </AlertDescription>
    </Alert>

    <Card>
      <CardContent class="space-y-4 pt-6">
        <!-- Two GET forms, not one: the filter and the jump box submit different
             parameters, and a single form would send an empty `goto` with every filter
             change. PJAX intercepts both and turns them into bookmarkable URLs.

             Neither carries `page`. A filter changed from page 7 must land on page 1,
             and omitting the field is how that happens by itself. -->
        <div class="flex flex-wrap items-end gap-3">
          <form :action="basePath" method="get" class="flex flex-wrap items-end gap-2">
            <label class="flex flex-col gap-1 text-xs text-muted-foreground">
              状态
              <select
                name="status"
                class="h-9 rounded-md border border-input bg-background px-2 text-sm"
              >
                <option value="">全部</option>
                <option
                  v-for="s in statuses"
                  :key="s.value"
                  :value="s.value"
                  :selected="s.value === filters.status"
                >{{ s.label }}</option>
              </select>
            </label>
            <label class="flex flex-col gap-1 text-xs text-muted-foreground">
              类型
              <select
                name="kind"
                class="h-9 rounded-md border border-input bg-background px-2 text-sm"
              >
                <option value="">全部</option>
                <option
                  v-for="k in kinds"
                  :key="k"
                  :value="k"
                  :selected="k === filters.kind"
                >{{ k }}</option>
              </select>
            </label>
            <Button type="submit" variant="secondary" size="sm">筛选</Button>
            <Button v-if="filtered" as="a" :href="basePath" variant="ghost" size="sm">
              清除
            </Button>
          </form>

          <!-- Jump to id instead of a text search: the only free text worth searching
               is the error message, and that is an unindexed scan of the biggest table
               here. Pasting an id out of a log line is what actually happens. -->
          <form :action="basePath" method="get" class="ml-auto flex items-end gap-2">
            <label class="flex flex-col gap-1 text-xs text-muted-foreground">
              按编号跳转
              <Input name="goto" inputmode="numeric" placeholder="#123" class="w-28" />
            </label>
            <Button type="submit" variant="secondary" size="sm">跳转</Button>
          </form>
        </div>

        <!-- Counts from the server, over the whole table rather than the page. -->
        <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <span v-for="s in statuses" :key="s.value">
            {{ s.label }} <span class="font-medium text-foreground">{{ counts[s.value] ?? 0 }}</span>
          </span>
        </div>

        <ServerTable
          :columns="columns"
          :data="items"
          :total="total"
          :page="page"
          :page-size="pageSize"
          :page-href="pageHref"
          :filtered="filtered"
        >
          <template #cell-id="{ row }">
            <a :href="`${basePath}/${row.id}`" class="font-medium hover:underline">
              {{ row.id }}
            </a>
          </template>

          <template #cell-kind="{ row }">
            <span class="font-mono text-xs">{{ row.kind }}</span>
            <!-- Per-row, not only in the banner: the banner says which kinds are
                 affected, this says which rows. -->
            <Badge v-if="!row.known_kind" variant="destructive" class="ml-2">无处理器</Badge>
          </template>

          <template #cell-status="{ row }">
            <StatusBadge :meta="statusMeta(row)" />
          </template>

          <template #cell-run_at="{ row }">
            <span class="text-xs text-muted-foreground">{{ row.run_at_text || '—' }}</span>
          </template>

          <template #cell-last_error="{ row }">
            <span
              v-if="row.last_error"
              class="line-clamp-1 text-xs text-destructive"
              :title="String(row.last_error)"
            >{{ row.last_error }}</span>
            <span v-else class="text-xs text-muted-foreground">—</span>
          </template>

          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}`">详情</DropdownMenuItem>
            <!-- w-full because a <button> is shrink-to-fit even as a flex container, so
                 without it the hover highlight stops at the text. -->
            <DropdownMenuItem
              v-if="actionsFor(row).retry"
              as="button"
              type="submit"
              class="w-full"
              :form="`retry-${row.id}`"
            >重试</DropdownMenuItem>
            <DropdownMenuItem
              v-if="actionsFor(row).run"
              as="button"
              type="submit"
              class="w-full"
              :form="`run-${row.id}`"
            >立即执行</DropdownMenuItem>
            <DropdownMenuItem
              v-if="actionsFor(row).cancel"
              as="button"
              type="submit"
              class="w-full"
              :form="`cancel-${row.id}`"
            >取消</DropdownMenuItem>
            <DropdownMenuItem
              v-if="actionsFor(row).delete"
              variant="destructive"
              @select="askDelete(row)"
            >删除</DropdownMenuItem>
          </template>

          <template #empty>还没有任务。</template>
        </ServerTable>

        <!-- One hidden form per row per action, outside the dropdown: a menu item cannot
             carry a POST body itself, and a form nested in the teleported dropdown
             content would not survive SSR. Same arrangement as the user list's status
             toggle. -->
        <template v-for="row in items" :key="row.id">
          <form
            v-if="actionsFor(row).retry"
            :id="`retry-${row.id}`"
            :action="`${basePath}/${row.id}/retry${returnQuery}`"
            method="post"
            class="hidden"
          ><CsrfField :token="csrfToken" /></form>
          <form
            v-if="actionsFor(row).run"
            :id="`run-${row.id}`"
            :action="`${basePath}/${row.id}/run${returnQuery}`"
            method="post"
            class="hidden"
          ><CsrfField :token="csrfToken" /></form>
          <form
            v-if="actionsFor(row).cancel"
            :id="`cancel-${row.id}`"
            :action="`${basePath}/${row.id}/cancel${returnQuery}`"
            method="post"
            class="hidden"
          ><CsrfField :token="csrfToken" /></form>
        </template>
      </CardContent>
    </Card>

    <!-- The only irreversible action, and so the only one behind a dialog. Retry, run and
         cancel are all recoverable state changes; putting them behind a confirmation
         would just train an operator to click through it. -->
    <ConfirmDialog
      :open="pending !== null"
      :title="`删除任务 #${pending?.id}？`"
      description="该任务及其全部执行记录将被永久删除。此操作不可撤销。"
      :action="`${basePath}/${pending?.id}/delete${returnQuery}`"
      confirm-label="删除"
      cancel-label="取消"
      :csrf-token="csrfToken"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
