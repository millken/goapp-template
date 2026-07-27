<script setup lang="ts">
// The media library, in three shapes: the standalone page (mode="manage"), the
// picker inside a dialog (mode="pick"), and the move-target chooser
// (mode="dirs"). One component rather than three because the listing, the
// breadcrumb and the pager are identical in all of them — only what a click
// means differs.
//
// Directory changes are component state, never navigation: routing them
// through Inertia would put every `cd` in the browser history, and the back
// button inside a modal would then mean "go up one folder" instead of "close".
import { computed, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, File, Folder, Upload } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export type FmEntry = {
  name: string
  path: string
  dir: boolean
  size: number
  mtime: number
  url: string
}
type Crumb = { name: string; path: string }

const props = withDefaults(
  defineProps<{
    basePath: string
    urlPrefix: string
    csrfToken?: string
    mode?: 'manage' | 'pick' | 'dirs'
  }>(),
  { mode: 'manage' },
)

const emit = defineEmits<{ (e: 'select', entry: FmEntry): void }>()

const path = ref('')
const query = ref('')
const page = ref(1)
const entries = ref<FmEntry[]>([])
const breadcrumb = ref<Crumb[]>([])
const total = ref(0)
const pageSize = ref(40)
const busy = ref(false)
const error = ref('')
const selected = ref<Set<string>>(new Set())
const newDirName = ref('')
const uploadInput = ref<HTMLInputElement | null>(null)

const visible = computed(() =>
  props.mode === 'dirs' ? entries.value.filter((e) => e.dir) : entries.value,
)
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const canMutate = computed(() => props.mode === 'manage')

async function refresh() {
  busy.value = true
  error.value = ''
  try {
    const params = new URLSearchParams({
      path: path.value,
      q: query.value,
      page: String(page.value),
    })
    const res = await fetch(`${props.basePath}/api/list?${params}`, {
      headers: { Accept: 'application/json' },
      credentials: 'same-origin',
    })
    const body = await res.json()
    if (!res.ok) {
      error.value = body?.error ?? '读取失败'
      return
    }
    entries.value = body.entries ?? []
    breadcrumb.value = body.breadcrumb ?? []
    total.value = body.total ?? 0
    pageSize.value = body.pageSize ?? 40
    selected.value = new Set()
  } catch {
    error.value = '网络错误'
  } finally {
    busy.value = false
  }
}

// The token is what makes a mutation legal (see csrf.go); a missing one is a
// 403 the user cannot act on, so the buttons are disabled without it.
async function mutate(action: string, body: unknown): Promise<Record<string, unknown> | null> {
  busy.value = true
  error.value = ''
  try {
    const res = await fetch(`${props.basePath}/api/${action}`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: {
        'Content-Type': 'application/json',
        'X-CSRF-Token': props.csrfToken ?? '',
      },
      body: JSON.stringify(body),
    })
    const out = await res.json().catch(() => null)
    if (!res.ok) {
      error.value = (out as { error?: string })?.error ?? '操作失败'
      return null
    }
    const fails = (out as { errors?: { name: string; error: string }[] })?.errors ?? []
    if (fails.length) {
      error.value = fails.map((f) => `${f.name}：${f.error}`).join('；')
    }
    await refresh()
    return out as Record<string, unknown>
  } catch {
    error.value = '网络错误'
    return null
  } finally {
    busy.value = false
  }
}

function enter(entry: FmEntry) {
  if (entry.dir) {
    path.value = entry.path
    page.value = 1
    return
  }
  if (props.mode === 'pick') emit('select', entry)
}

function go(to: string) {
  path.value = to
  page.value = 1
}

function toggle(entry: FmEntry) {
  const next = new Set(selected.value)
  if (next.has(entry.path)) next.delete(entry.path)
  else next.add(entry.path)
  selected.value = next
}

async function mkdir() {
  const name = newDirName.value.trim()
  if (!name) return
  await mutate('mkdir', { path: path.value, name })
  newDirName.value = ''
}

async function removeSelected() {
  if (!selected.value.size) return
  await mutate('delete', { paths: [...selected.value] })
}

async function rename(entry: FmEntry) {
  const name = window.prompt('新名称', entry.name)?.trim()
  if (!name || name === entry.name) return
  await mutate('rename', { path: entry.path, name })
}

async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = input.files
  if (!files?.length) return
  const form = new FormData()
  for (const f of files) form.append('files', f)

  busy.value = true
  error.value = ''
  try {
    const res = await fetch(
      `${props.basePath}/api/upload?path=${encodeURIComponent(path.value)}`,
      {
        method: 'POST',
        credentials: 'same-origin',
        // Content-Type is deliberately unset: the browser adds the multipart
        // boundary, and setting it by hand produces a body the server cannot
        // parse.
        headers: { 'X-CSRF-Token': props.csrfToken ?? '' },
        body: form,
      },
    )
    const out = await res.json().catch(() => null)
    if (!res.ok) {
      error.value = (out as { error?: string })?.error ?? '上传失败'
    } else {
      const fails = (out as { errors?: { name: string; error: string }[] })?.errors ?? []
      if (fails.length) error.value = fails.map((f) => `${f.name}：${f.error}`).join('；')
    }
    await refresh()
  } catch {
    error.value = '网络错误'
  } finally {
    busy.value = false
    input.value = ''
  }
}

watch([path, page], refresh, { immediate: true })
watch(query, () => {
  page.value = 1
  refresh()
})
</script>

<template>
  <div class="space-y-3">
    <div class="flex flex-wrap items-center gap-2">
      <nav class="flex items-center gap-1 text-sm text-muted-foreground">
        <template v-for="(crumb, i) in breadcrumb" :key="crumb.path">
          <span v-if="i > 0">/</span>
          <button type="button" class="hover:text-foreground" @click="go(crumb.path)">
            {{ crumb.name }}
          </button>
        </template>
      </nav>

      <div class="ml-auto flex items-center gap-2">
        <Input
          v-model="query"
          placeholder="搜索当前目录"
          class="h-9 w-48"
          data-testid="search"
        />
        <template v-if="canMutate">
          <Input
            v-model="newDirName"
            placeholder="新目录名"
            class="h-9 w-32"
            data-testid="mkdir-name"
          />
          <Button type="button" variant="outline" :disabled="busy" @click="mkdir">
            新建目录
          </Button>
          <Button type="button" variant="outline" :disabled="busy" @click="uploadInput?.click()">
            <Upload class="mr-1 size-4" /> 上传
          </Button>
          <input
            ref="uploadInput"
            type="file"
            multiple
            class="hidden"
            data-testid="upload-input"
            @change="upload"
          />
          <Button
            type="button"
            variant="destructive"
            :disabled="busy || !selected.size"
            @click="removeSelected"
          >
            删除
          </Button>
        </template>
      </div>
    </div>

    <p v-if="error" class="text-sm text-destructive">{{ error }}</p>

    <ul class="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-6">
      <li
        v-for="entry in visible"
        :key="entry.path"
        :data-entry="entry.path"
        class="group cursor-pointer rounded-md border p-2 text-center hover:bg-accent"
        :class="selected.has(entry.path) ? 'ring-2 ring-primary' : ''"
        @click="enter(entry)"
      >
        <div class="flex h-20 items-center justify-center overflow-hidden">
          <Folder v-if="entry.dir" class="size-10 text-muted-foreground" />
          <img
            v-else-if="/\.(png|jpe?g|gif|webp)$/i.test(entry.name)"
            :src="entry.url"
            :alt="entry.name"
            loading="lazy"
            class="max-h-20 max-w-full object-contain"
          />
          <File v-else class="size-10 text-muted-foreground" />
        </div>
        <p class="mt-1 truncate text-xs" :title="entry.name">{{ entry.name }}</p>
        <div v-if="canMutate" class="mt-1 flex justify-center gap-2 text-xs">
          <button
            type="button"
            class="text-muted-foreground hover:text-foreground"
            @click.stop="toggle(entry)"
          >
            {{ selected.has(entry.path) ? '取消选择' : '选择' }}
          </button>
          <button
            type="button"
            class="text-muted-foreground hover:text-foreground"
            @click.stop="rename(entry)"
          >
            重命名
          </button>
        </div>
      </li>
    </ul>

    <p v-if="!visible.length && !busy" class="py-8 text-center text-sm text-muted-foreground">
      这个目录是空的
    </p>

    <div class="flex items-center justify-end gap-2 text-sm text-muted-foreground">
      <span>共 {{ total }} 项</span>
      <Button
        type="button"
        variant="outline"
        size="icon"
        class="size-9"
        data-testid="prev-page"
        :disabled="page <= 1"
        @click="page = page - 1"
      >
        <ChevronLeft class="size-4" />
      </Button>
      <Button
        type="button"
        variant="outline"
        size="icon"
        class="size-9"
        data-testid="next-page"
        :disabled="page >= pages"
        @click="page = page + 1"
      >
        <ChevronRight class="size-4" />
      </Button>
    </div>
  </div>
</template>
