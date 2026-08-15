<script setup lang="ts">
// One task in full, with its failure log — the page somebody opens because something
// went wrong.
//
// Three cards stacked, not tabs. A tab would put the failure log one click away, and the
// failure log is the reason for the visit.
//
// The two collapsible sections use the native <details> element rather than a component:
// nothing needs installing, and unlike a Dialog or a DropdownMenu it is not teleported,
// so it renders under SSR and hydrates without surprises.
import { computed, ref } from 'vue'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import CsrfField from '@/components/admin/CsrfField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import StatusBadge from '@/components/admin/StatusBadge.vue'
import { attemptOutcomeMeta, taskActions, taskStatusMeta } from '@/lib/task-status'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'

interface MenuItem { title: string; path: string; order?: number; section?: string }

type TaskDetail = {
  id: number
  kind: string
  status: string
  attempts: number
  max_attempts: number
  priority: number
  run_at: number
  run_at_text: string
  created_at: number
  created_at_text: string
  started_at: number
  started_at_text: string
  finished_at: number
  finished_at_text: string
  lease_until: number
  lease_until_text: string
  worker: string
  unique_key: string
  payload: string
  timeout_ms: number
  full_error: string
  schedule_id: number
  known_kind: boolean
}

type Attempt = {
  attempt: number
  outcome: string
  worker: string
  error: string
  started_at: number
  started_at_text: string
  finished_at: number
  finished_at_text: string
  duration_ms: number
}

const props = defineProps<{
  item: TaskDetail
  attempts: Attempt[]
  attemptTotal: number
  attemptCap: number
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

const meta = computed(() => taskStatusMeta(props.item.status, props.item.attempts, props.item.max_attempts))

// The payload arrives as the raw string the column holds, not as a parsed object: the
// column is TEXT and may not be valid JSON at all, and round-tripping the raw text is
// what guarantees nothing is lost on the way here. Pretty-printing is best-effort for
// the same reason.
const prettyPayload = computed(() => {
  try {
    return JSON.stringify(JSON.parse(props.item.payload), null, 2)
  } catch {
    return props.item.payload
  }
})
// Split once. The template needs the line count too, and re-splitting there would be a
// second pass over a blob whose size is the reason the collapsible exists.
const payloadLines = computed(() => prettyPayload.value.split('\n').length)
const payloadIsLong = computed(() => payloadLines.value > 20)

// From the shared table, not restated here: the list page hides the same buttons by the
// same rule, and two copies is how the two screens come to disagree about one task.
const can = computed(() => taskActions(props.item.status))

// back=detail tells the handler to return here rather than to the list. The handler
// rebuilds the target from keys it recognises, so this is a hint, not a redirect target.
const detailReturn = '?back=detail'

// Overlays start closed: SSR does not emit teleported content.
const confirming = ref(false)

const durationText = (ms: number) => {
  if (ms <= 0) return '—'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
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
    :crumb="`#${item.id}`"
  >
    <PageHeader :title="`任务 #${item.id}`" :description="item.kind">
      <template #actions>
        <!-- Each action is its own tiny form. Buttons cannot carry a POST body, and
             wrapping several submits in one form would need a name/value dance to tell
             them apart — separate forms are shorter and unambiguous. -->
        <form
          v-if="can.retry"
          :action="`${basePath}/${item.id}/retry${detailReturn}`"
          method="post"
          class="inline"
        >
          <CsrfField :token="csrfToken" />
          <Button type="submit" variant="outline" size="sm">重试</Button>
        </form>
        <form
          v-if="can.run"
          :action="`${basePath}/${item.id}/run${detailReturn}`"
          method="post"
          class="inline"
        >
          <CsrfField :token="csrfToken" />
          <Button type="submit" variant="outline" size="sm">立即执行</Button>
        </form>
        <form
          v-if="can.cancel"
          :action="`${basePath}/${item.id}/cancel${detailReturn}`"
          method="post"
          class="inline"
        >
          <CsrfField :token="csrfToken" />
          <Button type="submit" variant="outline" size="sm">取消</Button>
        </form>
        <Button
          v-if="can.delete"
          variant="destructive"
          size="sm"
          @click="confirming = true"
        >删除</Button>
      </template>
    </PageHeader>

    <!-- Retrying a task whose handler is gone would put it straight back into the same
         limbo, so the page says why before an operator tries. Same shape as the stale
         permission-key warning on the group form. -->
    <Alert v-if="!item.known_kind" variant="destructive" class="mb-4">
      <AlertDescription>
        类型 <code class="font-mono">{{ item.kind }}</code>
        在当前进程中没有注册处理器，即使重试也不会被执行。
      </AlertDescription>
    </Alert>

    <div class="space-y-4">
      <Card>
        <CardHeader><CardTitle class="text-base">概要</CardTitle></CardHeader>
        <CardContent>
          <dl class="grid grid-cols-[8rem_1fr] gap-y-2 text-sm">
            <dt class="text-muted-foreground">状态</dt>
            <dd><StatusBadge :meta="meta" /></dd>

            <dt class="text-muted-foreground">类型</dt>
            <dd class="font-mono text-xs">{{ item.kind }}</dd>

            <dt class="text-muted-foreground">尝试次数</dt>
            <dd>{{ item.attempts }} / {{ item.max_attempts }}</dd>

            <dt class="text-muted-foreground">计划执行</dt>
            <dd>{{ item.run_at_text || '—' }}</dd>

            <dt class="text-muted-foreground">创建于</dt>
            <dd>{{ item.created_at_text || '—' }}</dd>

            <dt class="text-muted-foreground">最近开始</dt>
            <dd>{{ item.started_at_text || '—' }}</dd>

            <dt class="text-muted-foreground">结束于</dt>
            <dd>{{ item.finished_at_text || '—' }}</dd>

            <!-- Only while running, and the single most useful field then: it is the
                 answer to "has the worker died, or is it still working?" -->
            <template v-if="item.status === 'running'">
              <dt class="text-muted-foreground">租约到期</dt>
              <dd>{{ item.lease_until_text || '—' }}</dd>
              <dt class="text-muted-foreground">执行者</dt>
              <dd class="font-mono text-xs break-all">{{ item.worker || '—' }}</dd>
            </template>

            <template v-if="item.schedule_id">
              <dt class="text-muted-foreground">来源</dt>
              <dd>定时任务 #{{ item.schedule_id }}</dd>
            </template>

            <template v-if="item.unique_key">
              <dt class="text-muted-foreground">幂等键</dt>
              <dd class="font-mono text-xs break-all">{{ item.unique_key }}</dd>
            </template>
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle class="text-base">载荷</CardTitle></CardHeader>
        <CardContent>
          <!-- Collapsed past twenty lines, expanded below that: a small payload behind a
               click is friction, a large one pushes the failure log off the screen. -->
          <details v-if="payloadIsLong">
            <summary class="cursor-pointer text-sm text-muted-foreground">
              展开载荷（{{ payloadLines }} 行）
            </summary>
            <pre class="mt-2 max-h-96 overflow-auto rounded-md bg-muted p-3 text-xs">{{ prettyPayload }}</pre>
          </details>
          <pre
            v-else
            class="overflow-x-auto rounded-md bg-muted p-3 text-xs"
          >{{ prettyPayload }}</pre>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle class="text-base">执行记录</CardTitle>
        </CardHeader>
        <CardContent class="space-y-2">
          <!-- A table, not a timeline. These rows are the same handful of fields every
               time, and what an operator scans for is "which attempt did it start failing
               on" — a timeline spends twice the vertical space to say the same thing. -->
          <p v-if="attemptTotal > attempts.length" class="text-xs text-muted-foreground">
            共 {{ attemptTotal }} 次尝试，显示最近 {{ attemptCap }} 次。
          </p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead class="h-10 w-12 px-3">#</TableHead>
                <TableHead class="h-10 w-40 px-3">开始</TableHead>
                <TableHead class="h-10 w-40 px-3">结束</TableHead>
                <TableHead class="h-10 w-20 px-3">耗时</TableHead>
                <TableHead class="h-10 w-28 px-3">结果</TableHead>
                <TableHead class="h-10 px-3">错误</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              <TableRow v-for="(a, i) in attempts" :key="`${a.attempt}-${i}`">
                <TableCell class="px-3 py-2.5">{{ a.attempt }}</TableCell>
                <TableCell class="px-3 py-2.5 text-xs whitespace-nowrap">
                  {{ a.started_at_text || '—' }}
                </TableCell>
                <TableCell class="px-3 py-2.5 text-xs whitespace-nowrap">
                  {{ a.finished_at_text || '—' }}
                </TableCell>
                <TableCell class="px-3 py-2.5 text-xs">{{ durationText(a.duration_ms) }}</TableCell>
                <TableCell class="px-3 py-2.5">
                  <StatusBadge :meta="attemptOutcomeMeta(a.outcome)" />
                </TableCell>
                <TableCell class="px-3 py-2.5">
                  <!-- A Go panic's stack is one long line per frame. Horizontal scrolling
                       inside a table cell makes it unreadable, so the full text wraps
                       (whitespace-pre-wrap keeps the newlines, break-all softens the long
                       frames) inside a height-capped scroller, with the first line as the
                       summary. `title` stands in for a tooltip component. -->
                  <details v-if="a.error">
                    <summary
                      class="cursor-pointer truncate text-xs text-destructive"
                      :title="a.error"
                    >{{ a.error.split('\n')[0] }}</summary>
                    <pre
                      class="mt-2 max-h-64 overflow-y-auto rounded bg-muted p-2 text-xs break-all whitespace-pre-wrap"
                    >{{ a.error }}</pre>
                  </details>
                  <span v-else class="text-xs text-muted-foreground">—</span>
                </TableCell>
              </TableRow>
              <TableEmpty v-if="!attempts.length" :colspan="6">
                还没有执行记录。
              </TableEmpty>
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>

    <ConfirmDialog
      :open="confirming"
      :title="`删除任务 #${item.id}？`"
      :description="`该任务及其 ${attemptTotal} 条执行记录将被永久删除。此操作不可撤销。`"
      :action="`${basePath}/${item.id}/delete`"
      confirm-label="删除"
      cancel-label="取消"
      :csrf-token="csrfToken"
      @update:open="(o) => (confirming = o)"
    />
  </AdminShell>
</template>
