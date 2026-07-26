<script setup lang="ts">
// The admin frame: icon rail (menu sections) + section panel (items of the
// active section) + topbar (breadcrumb | theme toggle + user menu) + flash +
// content. Two columns instead of a multi-level tree: each section gets a full
// column of items.
//
// Navigation stays plain <a href> — the PJAX layer intercepts through
// document-level delegation. Path awareness comes from the currentPath prop
// (injected by resolve, present under SSR too); location is only a client-side
// fallback, never touched at setup time — QuickJS has no location.
import { computed } from 'vue'
import { ChevronDown, FileText, Gauge, LogOut, Settings, Users } from 'lucide-vue-next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import ThemeToggle from '@/components/admin/ThemeToggle.vue'

interface MenuItem {
  title: string
  path: string
  order?: number
  section?: string
}

const props = defineProps<{
  menu?: MenuItem[]
  user?: { id?: number; username?: string }
  mount?: string
  currentPath?: string
  flash?: Record<string, string>
  crumb?: string
}>()

const base = computed(() => props.mount || '/admin')
const path = computed(() =>
  props.currentPath ?? (typeof location === 'undefined' ? base.value : location.pathname),
)

// Home is hardcoded, not a registered menu item: the dashboard is an exempt
// route, so every signed-in user sees it regardless of permission filtering.
const sections = computed(() => {
  const home = { title: 'Home', items: [{ title: 'Overview', path: base.value }] }
  const out: { title: string; items: MenuItem[] }[] = [home]
  // Seeded with Home already at 0, so an item registered under that section name
  // joins the built-in column instead of pushing a second one with the same title.
  const idx = new Map<string, number>([[home.title, 0]])
  for (const item of props.menu ?? []) {
    const name = item.section || 'Content'
    let i = idx.get(name)
    if (i === undefined) {
      i = out.length
      idx.set(name, i)
      out.push({ title: name, items: [] })
    }
    out[i].items.push(item)
  }
  return out
})

// An item is active when the path equals it or extends it with a slash, so
// /admin/user/3/edit lights up Users. Longest match wins; no match → Home.
const active = computed(() => {
  let best: { section: number; item: MenuItem } | null = null
  sections.value.forEach((s, si) => {
    for (const it of s.items) {
      const hit = path.value === it.path || path.value.startsWith(it.path + '/')
      if (hit && (!best || it.path.length > best.item.path.length)) {
        best = { section: si, item: it }
      }
    }
  })
  return best ?? { section: 0, item: sections.value[0].items[0] }
})

const crumbs = computed(() => {
  const s = sections.value[active.value.section]
  const list: { title: string; path?: string }[] = [
    { title: s.title, path: s.items[0].path },
    { title: active.value.item.title, path: props.crumb ? active.value.item.path : undefined },
  ]
  if (props.crumb) list.push({ title: props.crumb })
  return list
})

const sectionIcons: Record<string, unknown> = {
  Home: Gauge,
  Content: FileText,
  Access: Users,
  System: Settings,
}
const iconFor = (title: string) => sectionIcons[title] ?? FileText

// One-shot messages staged by the server before a redirect (sess.Flash), keyed
// by kind. The session middleware consumes them, so they vanish on the next
// navigation — no dismiss button needed.
const flashVariant = (kind: string) =>
  kind === 'error' ? 'destructive' : kind === 'success' ? 'success' : 'default'
</script>

<template>
  <div class="flex min-h-screen bg-muted/40">
    <!-- Icon rail: one button per section -->
    <aside class="flex w-[4.5rem] shrink-0 flex-col gap-1 border-r bg-background p-2">
      <div
        class="mx-auto mb-3 flex size-9 items-center justify-center rounded-md bg-primary font-bold text-primary-foreground"
      >A</div>
      <a
        v-for="(s, si) in sections"
        :key="s.title"
        :href="s.items[0].path"
        class="flex flex-col items-center gap-1 rounded-md px-1 py-2 text-[11px] leading-none"
        :class="si === active.section
          ? 'bg-accent font-medium text-foreground'
          : 'text-muted-foreground hover:bg-accent hover:text-foreground'"
      >
        <component :is="iconFor(s.title)" class="size-[18px]" />
        {{ s.title }}
      </a>
    </aside>

    <!-- Section panel: the active section's items -->
    <aside class="flex w-50 shrink-0 flex-col gap-1 border-r bg-background p-3">
      <div class="px-3 pb-3 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {{ sections[active.section].title }}
      </div>
      <Button
        v-for="item in sections[active.section].items"
        :key="item.path"
        as="a"
        :href="item.path"
        variant="ghost"
        class="w-full justify-start"
        :class="item.path === active.item.path ? 'bg-accent font-medium' : ''"
      >{{ item.title }}</Button>
    </aside>

    <div class="flex min-w-0 flex-1 flex-col">
      <!-- Topbar: breadcrumb | theme toggle + user menu -->
      <header class="flex h-14 shrink-0 items-center justify-between gap-4 border-b bg-background px-6">
        <nav aria-label="Breadcrumb" class="flex min-w-0 items-center gap-2 text-sm">
          <template v-for="(c, i) in crumbs" :key="i">
            <span v-if="i > 0" class="text-muted-foreground/50">/</span>
            <a v-if="c.path" :href="c.path" class="text-muted-foreground hover:text-foreground hover:underline">
              {{ c.title }}
            </a>
            <span v-else class="truncate font-medium">{{ c.title }}</span>
          </template>
        </nav>
        <div class="flex items-center gap-1">
          <ThemeToggle />
          <DropdownMenu>
            <DropdownMenuTrigger as-child>
              <Button variant="ghost" size="sm">
                {{ user?.username ?? 'account' }}
                <ChevronDown class="size-3.5" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel class="font-normal text-muted-foreground">
                Signed in as <span class="font-medium text-foreground">{{ user?.username }}</span>
              </DropdownMenuLabel>
              <DropdownMenuSeparator />
              <form :action="`${base}/logout`" method="post">
                <DropdownMenuItem as="button" type="submit" class="w-full">
                  <LogOut />
                  Log out
                </DropdownMenuItem>
              </form>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      <main class="p-8">
        <Alert
          v-for="(message, kind) in flash || {}"
          :key="kind"
          :variant="flashVariant(kind)"
          class="mb-4"
        >
          <AlertDescription>{{ message }}</AlertDescription>
        </Alert>
        <slot />
      </main>
    </div>
  </div>
</template>
