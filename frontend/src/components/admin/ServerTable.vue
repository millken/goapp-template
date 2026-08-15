<script setup lang="ts">
// The list table for a table the server pages.
//
// DataTable.vue is the one to use for anything that fits in memory. This exists for
// the one that does not, and the difference is not a flag on that component — it is
// that every number it shows has to come from the server:
//
//   * DataTable's footer reads `data.length`. Hand it the 25 rows of page one out of
//     half a million and it says "共 25 条". It does not degrade, it lies.
//   * Its search box filters the rows it holds, so on a paged list it would search
//     one page and report a total for the whole table.
//   * Paging has to be links. PJAX turns an <a> into a navigation and a GET <form>
//     into a bookmarkable URL, which is what makes a filtered list shareable and
//     costs zero lines of JavaScript. DataTable's pager is reka-ui emitting
//     update:page — different DOM, not a different prop.
//
// So: no @tanstack/vue-table, no client state at all. Row order and row selection
// belong to the server, and this component has nothing that could disagree with it.
//
// Deliberately absent, both because the page owns them instead:
//   * searchKey — filtering is the page's own GET form
//   * sortable  — v1 sorts by id descending; every sort key needs a matching index,
//                 so the seam is the handler's ORDER BY, not a header click
//
// The header and cell classes are copied verbatim from DataTable.vue so the two read
// as one table at a glance.
import { computed } from 'vue'
import { ChevronLeft, ChevronRight, MoreHorizontal } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  Table, TableBody, TableCell, TableEmpty, TableHead, TableHeader, TableRow,
} from '@/components/ui/table'

export interface ServerTableColumn {
  key: string
  label: string
  /** Extra classes for this column's cells — e.g. 'w-24 whitespace-nowrap'. */
  class?: string
}
type Row = Record<string, unknown>

const props = withDefaults(defineProps<{
  columns: ServerTableColumn[]
  /** The current page's rows, already ordered and sliced by the server. */
  data: Row[]
  /** Total matching rows, from the server. Never data.length. */
  total: number
  page: number
  pageSize: number
  /**
   * Builds the URL for a page. The page owns it because it is the page that knows
   * which filters have to survive the click.
   */
  pageHref: (page: number) => string
  /**
   * Whether a filter is in effect, which the component cannot see for itself. It
   * only changes which empty state is shown, and showing the wrong one is the
   * difference between "there is nothing here" and "your filter matched nothing".
   */
  filtered?: boolean
}>(), { filtered: false })

const slots = defineSlots<
  { [K: `cell-${string}`]: (p: { row: Row }) => unknown }
  & { 'row-actions'?: (p: { row: Row }) => unknown; empty?: () => unknown }
>()

const colspan = computed(() => props.columns.length + (slots['row-actions'] ? 1 : 0))
const pageCount = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
const hasPrev = computed(() => props.page > 1)
const hasNext = computed(() => props.page < pageCount.value)
</script>

<template>
  <div class="space-y-4">
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead v-for="c in columns" :key="c.key" class="h-10 px-3" :class="c.class">
            {{ c.label }}
          </TableHead>
          <TableHead v-if="slots['row-actions']" class="h-10 w-12 px-3" />
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="(row, i) in data" :key="String(row.id ?? i)">
          <TableCell v-for="c in columns" :key="c.key" class="px-3 py-2.5" :class="c.class">
            <slot :name="`cell-${c.key}`" :row="row">{{ row[c.key] }}</slot>
          </TableCell>
          <TableCell v-if="slots['row-actions']" class="px-3 py-2.5">
            <DropdownMenu>
              <DropdownMenuTrigger as-child>
                <Button variant="ghost" size="icon-sm"><MoreHorizontal /></Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <slot name="row-actions" :row="row" />
              </DropdownMenuContent>
            </DropdownMenu>
          </TableCell>
        </TableRow>
        <!-- Two different emptinesses, same distinction DataTable draws: the #empty
             slot is the page's "nothing here yet" copy, which would read as a lie
             when the table is full and the filter simply matched none of it. This
             component cannot tell them apart on its own, hence the `filtered` prop. -->
        <TableEmpty v-if="!data.length" :colspan="colspan">
          <slot v-if="!filtered" name="empty">还没有记录。</slot>
          <template v-else>没有匹配该筛选条件的记录。</template>
        </TableEmpty>
      </TableBody>
    </Table>

    <!-- The count is always shown, from `total`; the pager only when there is
         somewhere to go. A footer that vanishes leaves "how many are there?"
         unanswered, which is the first question a filtered list raises.

         Arrows only, matching FileManager.vue's pager and the approved design.
         Each is a <Button as="a">, which renders an <a> — never an <a> wrapping a
         <button>, which browsers reparse and hydration then disagrees with, and
         which ssr_newpages_test.go's maxButtonDepth check catches.

         At the ends the arrow becomes a <span>, not a disabled <a>: there is no such
         thing as a disabled anchor, and one that looks disabled but still navigates
         is worse than one that is plainly inert. -->
    <div class="flex items-center justify-between gap-4">
      <span class="shrink-0 text-sm whitespace-nowrap text-muted-foreground">
        共 {{ total }} 条<template v-if="pageCount > 1">
          · 第 {{ page }} / {{ pageCount }} 页</template>
      </span>

      <div v-if="pageCount > 1" class="ml-auto flex items-center gap-1">
        <Button
          v-if="hasPrev"
          as="a"
          :href="pageHref(page - 1)"
          variant="outline"
          size="icon-sm"
          aria-label="上一页"
          data-testid="prev-page"
        >
          <ChevronLeft class="size-4" />
        </Button>
        <span
          v-else
          class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground/40"
          aria-hidden="true"
        >
          <ChevronLeft class="size-4" />
        </span>

        <Button
          v-if="hasNext"
          as="a"
          :href="pageHref(page + 1)"
          variant="outline"
          size="icon-sm"
          aria-label="下一页"
          data-testid="next-page"
        >
          <ChevronRight class="size-4" />
        </Button>
        <span
          v-else
          class="inline-flex size-8 items-center justify-center rounded-md text-muted-foreground/40"
          aria-hidden="true"
        >
          <ChevronRight class="size-4" />
        </span>
      </div>
    </div>
  </div>
</template>
