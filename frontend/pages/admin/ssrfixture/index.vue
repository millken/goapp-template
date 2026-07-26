<!--
  GENERATED FIXTURE — do not edit by hand.

  This is `goappctl gen admin ssrfixture`'s index.vue, committed verbatim so the
  generated admin list page reaches the SSR bundle. It has to exist as a real
  .vue file: the page it comes from is a Go template with [[ ]] delimiters, so it
  never lands in frontend/pages/ and no test could otherwise render the very
  components that carry QuickJS risk — Dialog, DropdownMenu, Select and the
  TanStack pager.

  Regenerate with:
      go run ./cmd/goappctl gen admin ssrfixture --force
      rm -rf internal/controller/adminssrfixture \
             frontend/pages/admin/ssrfixture/form.vue
      git checkout internal/controller/mount_gen.go

  TestAdminIndexFixtureIsCurrent fails if this drifts from the template.
  Nothing routes here; it exists only to be rendered by
  TestSSR_GeneratedAdminListRendersUnderQuickJS.
-->
<script setup lang="ts">
import { ref } from 'vue'
import { Plus } from 'lucide-vue-next'
import AdminShell from '@/components/admin/AdminShell.vue'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
import DataTable from '@/components/admin/DataTable.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { DropdownMenuItem } from '@/components/ui/dropdown-menu'

interface MenuItem { title: string; path: string; order?: number; section?: string }
// A type alias, not an interface: DataTable's `data` prop expects
// Record<string, unknown>[], and only type aliases (not interfaces) satisfy an
// index-signature target structurally.
type Ssrfixture = { id: number; name: string }

defineProps<{
  items: Ssrfixture[]
  basePath: string
  // Injected by the admin auth middleware for the shell:
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  // One-shot messages staged by the handlers before their redirects:
  flash?: Record<string, string>
}>()

const columns = [
  { key: 'id', label: 'ID' },
  { key: 'name', label: 'Name', sortable: true },
]

// The row awaiting delete confirmation; null closes the dialog. Overlays must
// start closed — SSR does not emit teleported content.
const pending = ref<Ssrfixture | null>(null)
const askDelete = (row: Record<string, unknown>) => {
  pending.value = row as unknown as Ssrfixture
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
    <PageHeader title="Ssrfixture">
      <template #actions>
        <Button as="a" :href="`${basePath}/new`" size="sm">
          <Plus />
          New
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
          <template #row-actions="{ row }">
            <DropdownMenuItem as="a" :href="`${basePath}/${row.id}/edit`">Edit</DropdownMenuItem>
            <DropdownMenuItem variant="destructive" @select="askDelete(row)">Delete</DropdownMenuItem>
          </template>
          <template #empty>No ssrfixture yet.</template>
        </DataTable>
      </CardContent>
    </Card>

    <!-- Delete posts to the real handler, so the server stays the single source
         of truth — no client-side mutation. -->
    <ConfirmDialog
      :open="pending !== null"
      :title="`Delete “${pending?.name}”?`"
      :action="`${basePath}/${pending?.id}/delete`"
      @update:open="(o) => !o && (pending = null)"
    />
  </AdminShell>
</template>
