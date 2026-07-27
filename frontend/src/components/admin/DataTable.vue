<script setup lang="ts">
// The list-page table: search, sort, client-side pagination, empty state and a
// row-actions dropdown, in one component so generated pages carry column defs
// instead of plumbing. Column defs are the simple spec below rather than raw
// TanStack ColumnDef — a page needing more edits this file (it is owned code,
// same as the shadcn components). Swap in server-side paging by adding query
// params to the handler and setting manualPagination here.
import { computed, h, ref } from 'vue'
import {
  FlexRender, getCoreRowModel, getFilteredRowModel, getPaginationRowModel,
  getSortedRowModel, useVueTable,
  type ColumnDef, type ColumnFiltersState, type SortingState,
} from '@tanstack/vue-table'
import { ArrowUpDown, MoreHorizontal } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import {
  Pagination, PaginationContent, PaginationEllipsis, PaginationFirst,
  PaginationItem, PaginationLast, PaginationNext, PaginationPrevious,
} from '@/components/ui/pagination'
import {
  Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'
import { valueUpdater } from '@/lib/utils'

export interface DataTableColumn {
  key: string
  label: string
  sortable?: boolean
}
type Row = Record<string, unknown>

const props = withDefaults(defineProps<{
  columns: DataTableColumn[]
  data: Row[]
  searchKey?: string
  pageSize?: number
}>(), { pageSize: 20 })

const slots = defineSlots<
  { [K: `cell-${string}`]: (p: { row: Row }) => unknown }
  & { 'row-actions'?: (p: { row: Row }) => unknown; empty?: () => unknown }
>()

const sorting = ref<SortingState>([])
const columnFilters = ref<ColumnFiltersState>([])

const columnDefs = computed<ColumnDef<Row>[]>(() =>
  props.columns.map((c) => ({
    accessorKey: c.key,
    header: c.sortable
      ? ({ column }) =>
          h(
            Button,
            {
              variant: 'ghost',
              class: '-ml-4',
              onClick: () => column.toggleSorting(column.getIsSorted() === 'asc'),
            },
            () => [c.label, h(ArrowUpDown, { class: 'ml-2 size-4' })],
          )
      : c.label,
  })),
)

const table = useVueTable({
  get data() { return props.data },
  get columns() { return columnDefs.value },
  getCoreRowModel: getCoreRowModel(),
  getSortedRowModel: getSortedRowModel(),
  getFilteredRowModel: getFilteredRowModel(),
  getPaginationRowModel: getPaginationRowModel(),
  onSortingChange: (u) => valueUpdater(u, sorting),
  onColumnFiltersChange: (u) => valueUpdater(u, columnFilters),
  initialState: { pagination: { pageSize: props.pageSize } },
  state: {
    get sorting() { return sorting.value },
    get columnFilters() { return columnFilters.value },
  },
})

const setFilter = (v: string | number) => {
  if (props.searchKey) table.getColumn(props.searchKey)?.setFilterValue(String(v))
}
const colspan = computed(() => props.columns.length + (slots['row-actions'] ? 1 : 0))

// The placeholder names the column the way the header does. searchKey is a field
// key — "name", "username" — and putting that in front of a user reads like a
// leak of the schema.
const searchLabel = computed(
  () => props.columns.find((c) => c.key === props.searchKey)?.label ?? props.searchKey,
)
</script>

<template>
  <div class="space-y-4">
    <Input
      v-if="searchKey"
      :placeholder="`按${searchLabel}筛选…`"
      class="max-w-xs"
      @update:model-value="setFilter"
    />

    <Table>
      <TableHeader>
        <TableRow v-for="hg in table.getHeaderGroups()" :key="hg.id">
          <TableHead v-for="header in hg.headers" :key="header.id">
            <FlexRender :render="header.column.columnDef.header" :props="header.getContext()" />
          </TableHead>
          <TableHead v-if="slots['row-actions']" class="w-12" />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="row in table.getRowModel().rows" :key="row.id">
          <TableCell v-for="cell in row.getVisibleCells()" :key="cell.id">
            <slot :name="`cell-${cell.column.id}`" :row="row.original">
              <FlexRender :render="cell.column.columnDef.cell" :props="cell.getContext()" />
            </slot>
          </TableCell>
          <TableCell v-if="slots['row-actions']">
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="icon-sm"><MoreHorizontal /></Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <slot name="row-actions" :row="row.original" />
              </DropdownMenuContent>
            </DropdownMenu>
          </TableCell>
        </TableRow>
        <!-- Two different emptinesses: the #empty slot is the table's own "nothing
             here yet" copy, which would read as a lie when 40 rows exist and the
             search simply matched none of them. -->
        <TableEmpty v-if="!table.getRowModel().rows.length" :colspan="colspan">
          <slot v-if="!data.length" name="empty">还没有记录。</slot>
          <template v-else>没有匹配该筛选条件的记录。</template>
        </TableEmpty>
      </TableBody>
    </Table>

    <!-- Page state lives in the table: reka-ui emits update:page and we forward
         it, so there is one source of truth. First/Previous/Next/Last are
         siblings of the page items, never parents — wrapping one in a
         PaginationItem nests a <button> inside a <button>, which browsers
         reparse and hydration then disagrees with. PaginationItem is itself a
         button (it applies buttonVariants), so the page number goes in its slot
         rather than in a nested Button. -->
    <Pagination
      v-if="table.getPageCount() > 1"
      :items-per-page="pageSize"
      :total="table.getFilteredRowModel().rows.length"
      :page="table.getState().pagination.pageIndex + 1"
      :sibling-count="1"
      show-edges
      @update:page="(p) => table.setPageIndex(p - 1)"
    >
      <PaginationContent v-slot="{ items }" class="justify-end">
        <PaginationFirst />
        <PaginationPrevious />
        <template v-for="(item, i) in items">
          <PaginationItem
            v-if="item.type === 'page'"
            :key="`page-${item.value}`"
            :value="item.value"
            :is-active="item.value === table.getState().pagination.pageIndex + 1"
          >{{ item.value }}</PaginationItem>
          <PaginationEllipsis v-else :key="`gap-${i}`" :index="i" />
        </template>
        <PaginationNext />
        <PaginationLast />
      </PaginationContent>
    </Pagination>
  </div>
</template>
