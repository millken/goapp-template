<script setup lang="ts">
// Editing a cron plan's expression — the only field an operator owns here.
//
// A full page rather than an inline edit, because the area's convention is that
// create/edit are pages, and because internal/validate's contract is "re-render the same
// page with an `errors` prop". An inline row has no page to re-render and no channel for
// the error.
//
// The "next runs" preview is computed by the server, from the same parser the scheduler
// runs on. Previewing as you type would need either a cron parser in JavaScript (a new
// npm dependency, and a second implementation of the one thing that must not have two) or
// a preview endpoint with its own response shape. Neither is worth more than looking after
// saving.
import AdminShell from '@/components/admin/AdminShell.vue'
import CsrfField from '@/components/admin/CsrfField.vue'
import FormField from '@/components/admin/FormField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

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
  next_run_at_text: string
  last_fire_at_text: string
}

defineProps<{
  item: CronRow
  // The value the input shows, which for a rejected submission is what was typed rather
  // than what is stored — the same reason the group form rebinds onto the submitted item.
  expression: string
  nextRuns: string[]
  basePath: string
  errors?: Record<string, string>
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string; avatar?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
  csrfToken?: string
  urlPrefix?: string
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
    :url-prefix="urlPrefix"
    crumb="编辑"
  >
    <PageHeader :title="`编辑定时任务：${item.name}`" />

    <!-- max-w-lg, the one exception to the full-width content area: an input stretched
         across a wide screen is harder to use, not easier. -->
    <Card class="max-w-lg">
      <CardContent class="pt-6">
        <form :action="`${basePath}/${item.id}`" method="post" class="space-y-4">
          <CsrfField :token="csrfToken" />

          <!-- Read-only text, NOT a disabled input. A disabled input submits nothing and
               still looks like a field, which is a small lie about what can be changed;
               plain text says it plainly. -->
          <FormField name="kind" label="处理器">
            <p class="font-mono text-sm">{{ item.kind }}</p>
            <p class="text-xs text-muted-foreground">由代码注册，不可在此修改。</p>
          </FormField>

          <FormField name="expression" label="cron 表达式" :error="errors?.expression">
            <Input
              id="expression"
              name="expression"
              :model-value="expression"
              class="font-mono"
              :aria-invalid="!!errors?.expression"
            />
            <p class="text-xs text-muted-foreground">
              五字段（分 时 日 月 星期），也支持
              <code class="font-mono">@daily</code>、<code class="font-mono">@hourly</code>
              等简写和 <code class="font-mono">@every 30s</code>。
            </p>
            <!-- Shown whenever the code's default differs, so the reset action below is
                 never a leap of faith. -->
            <p v-if="item.drifted" class="text-xs text-muted-foreground">
              代码默认：<code class="font-mono">{{ item.code_spec }}</code>
            </p>
          </FormField>

          <FormField name="nextRuns" label="接下来的执行时间">
            <!-- Empty when the expression does not parse: the field error above is the
                 thing to read then. -->
            <ul v-if="nextRuns.length" class="space-y-1 text-sm">
              <li v-for="(t, i) in nextRuns" :key="i" class="font-mono text-xs">{{ t }}</li>
            </ul>
            <p v-else class="text-xs text-muted-foreground">
              表达式无法解析，或没有可预期的执行时间。
            </p>
            <p v-if="!item.enabled" class="text-xs text-muted-foreground">
              该计划当前已暂停，即使保存了新的表达式也不会自动执行。
            </p>
          </FormField>

          <div class="flex gap-2">
            <Button type="submit">保存</Button>
            <Button as="a" :href="basePath" variant="outline">取消</Button>
          </div>
        </form>

        <!-- Its own form, outside the one above: a second submit inside it would need a
             name/value dance to be told apart, and this is not "save the expression" —
             it is "discard my override". -->
        <form
          v-if="item.drifted"
          :action="`${basePath}/${item.id}/reset`"
          method="post"
          class="mt-4 border-t pt-4"
        >
          <CsrfField :token="csrfToken" />
          <Button type="submit" variant="ghost" size="sm">恢复为代码默认值</Button>
        </form>
      </CardContent>
    </Card>
  </AdminShell>
</template>
