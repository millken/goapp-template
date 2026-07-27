<script setup lang="ts">
// The permission grid: one row per registered resource, two checkboxes each.
// `modify` implies `access` one-way, mirroring permSet.Allows on the server —
// this is feedback, not enforcement; the server normalises the same way on save.
import { reactive, watch } from 'vue'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

type PermRow = { resource: string; access: boolean; modify: boolean }

const props = defineProps<{ rows: PermRow[]; stale?: string[]; superuser?: boolean }>()

// A local copy so the implication can be applied as the user clicks.
const state = reactive<Record<string, { access: boolean; modify: boolean }>>({})
watch(
  () => props.rows,
  (rows) => {
    for (const r of rows) state[r.resource] = { access: r.access, modify: r.modify }
  },
  { immediate: true, deep: true },
)

function onModify(resource: string) {
  if (state[resource].modify) state[resource].access = true
}
function onAccess(resource: string) {
  if (!state[resource].access) state[resource].modify = false
}
</script>

<template>
  <div class="space-y-3">
    <p v-if="superuser" class="text-sm text-muted-foreground">
      该分组是超级管理员，绕过所有权限检查 —— 下面的勾选不影响它的实际权限。
    </p>

    <div class="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>资源</TableHead>
            <TableHead class="w-24 text-center">读取</TableHead>
            <TableHead class="w-24 text-center">修改</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="r in rows" :key="r.resource">
            <TableCell><code class="text-sm">{{ r.resource }}</code></TableCell>
            <TableCell class="text-center">
              <input
                type="checkbox"
                name="permissions"
                class="size-4 accent-primary"
                :value="`${r.resource}.access`"
                :data-resource="r.resource"
                data-verb="access"
                :checked="state[r.resource]?.access"
                @change="state[r.resource].access = ($event.target as HTMLInputElement).checked; onAccess(r.resource)"
              >
            </TableCell>
            <TableCell class="text-center">
              <input
                type="checkbox"
                name="permissions"
                class="size-4 accent-primary"
                :value="`${r.resource}.modify`"
                :data-resource="r.resource"
                data-verb="modify"
                :checked="state[r.resource]?.modify"
                @change="state[r.resource].modify = ($event.target as HTMLInputElement).checked; onModify(r.resource)"
              >
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>

    <p v-if="stale?.length" class="text-sm text-muted-foreground">
      保存将清除以下失效权限（对应的资源已不存在）：
      <code v-for="k in stale" :key="k" class="ml-1">{{ k }}</code>
    </p>
  </div>
</template>
