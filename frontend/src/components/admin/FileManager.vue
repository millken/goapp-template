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
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, File, Folder, Upload } from 'lucide-vue-next'
import ConfirmDialog from '@/components/admin/ConfirmDialog.vue'
// FileManagerDialog itself renders a FileManager (for its pick/dirs modes),
// so this is a two-file import cycle — FileManager -> FileManagerDialog ->
// FileManager. Harmless: both sides only touch the import inside their
// <template>, which runs long after both modules have finished evaluating,
// never at module-init time.
import FileManagerDialog from '@/components/admin/FileManagerDialog.vue'
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
const confirmingDelete = ref(false)
const moveOpen = ref(false)

const visible = computed(() =>
  props.mode === 'dirs' ? entries.value.filter((e) => e.dir) : entries.value,
)
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))
const canMutate = computed(() => props.mode === 'manage')

// Spec §8's accepted trade-off: no trash, no undo — a confirm dialog naming
// what is about to go is the only guard. Delete is recursive, so a selected
// directory has to be called out specifically: "3 items" undersells it when
// one of the three is a folder full of other files.
const selectedHasDir = computed(() =>
  entries.value.some((e) => selected.value.has(e.path) && e.dir),
)
const deleteTitle = computed(() => `删除所选的 ${selected.value.size} 项？`)
const deleteDescription = computed(() => {
  const base = `将永久删除 ${selected.value.size} 项，此操作不可撤销。`
  return selectedHasDir.value
    ? `${base}其中包含目录，目录内的全部内容都会一并删除。`
    : base
})

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

// The token is what makes a mutation legal (see csrf.go). Nothing here
// disables the buttons on a missing token, though — omitting it just means
// the request 403s, and the user sees that as the generic 操作失败 below.
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
    const message = fails.length ? fails.map((f) => `${f.name}：${f.error}`).join('；') : ''
    // refresh() clears `error` as its first statement, before its first
    // `await` — so setting the per-item message before calling it would wipe
    // the message out in the same synchronous stack, before any render ever
    // observes it. Set it only after refresh() has settled.
    await refresh()
    if (message) error.value = message
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

// removeSelected only opens the confirmation — see performDelete for the
// actual mutation, run once the dialog's own confirm button is clicked.
function removeSelected() {
  if (!selected.value.size) return
  confirmingDelete.value = true
}

async function performDelete() {
  confirmingDelete.value = false
  if (!selected.value.size) return
  await mutate('delete', { paths: [...selected.value] })
}

async function rename(entry: FmEntry) {
  const name = window.prompt('新名称', entry.name)?.trim()
  if (!name || name === entry.name) return
  // A 409 here ("同名项已存在") arrives as a plain body.error, which mutate()
  // already surfaces through `error` below — no special case needed.
  await mutate('rename', { path: entry.path, name })
}

function openMove() {
  if (!selected.value.size) return
  moveOpen.value = true
}

// The target is whatever FileManagerDialog's dirs-mode picker emits — either
// a subfolder entered and chosen, or the current directory itself (including
// the root; see FileManager's own "选择此目录" control below, the only way to
// move something back out of a folder). A collision comes back as a per-item
// error, which mutate() already turns into the same `f.name：f.error` message
// mkdir and upload use — no special case needed here either.
async function performMove(target: FmEntry) {
  moveOpen.value = false
  if (!selected.value.size) return
  await mutate('move', { paths: [...selected.value], to: target.path })
}

// dirs mode has no file entries to click (see `visible` above), so the only
// way to pick a destination is a dedicated control for "the folder I'm
// looking at right now" — otherwise the root, and any folder without a
// subfolder of its own, could never be chosen at all.
function chooseCurrent() {
  const name = breadcrumb.value.at(-1)?.name ?? '全部文件'
  emit('select', { name, path: path.value, dir: true, size: 0, mtime: 0, url: '' })
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
    let message = ''
    if (!res.ok) {
      message = (out as { error?: string })?.error ?? '上传失败'
    } else {
      const fails = (out as { errors?: { name: string; error: string }[] })?.errors ?? []
      if (fails.length) message = fails.map((f) => `${f.name}：${f.error}`).join('；')
    }
    // See mutate()'s comment: refresh() clears `error` before its first
    // `await`, so the per-item message has to be applied after it settles.
    await refresh()
    if (message) error.value = message
  } catch {
    error.value = '网络错误'
  } finally {
    busy.value = false
    input.value = ''
  }
}

// Not `{ immediate: true }`: an immediate watcher fires during setup, which
// runs under SSR too — and QuickJS has no fetch, so refresh() would throw and
// the server would render a network-error banner it never earned (it never
// tried the request). onMounted only runs client-side, which is exactly the
// "we're alive in a browser" signal this needs, with no environment sniffing.
onMounted(refresh)
watch([path, page], refresh)

// Debounced, and careful not to double up with the watcher above. Typing
// "photo" is five keystrokes; without a debounce that is five requests, one
// per character. And a plain `page.value = 1; refresh()` here — the shape
// this replaced — fires refresh() directly *and* triggers the [path, page]
// watcher whenever page.value actually changes (i.e. it wasn't 1 already),
// which is exactly the case a search typed from page 2 or later hits: two
// concurrent, identical requests. Setting page.value = 1 and calling refresh()
// are kept mutually exclusive below so exactly one fires either way.
const SEARCH_DEBOUNCE_MS = 300
let searchTimer: ReturnType<typeof setTimeout> | undefined
watch(query, () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    if (page.value === 1) refresh()
    else page.value = 1
  }, SEARCH_DEBOUNCE_MS)
})
onUnmounted(() => clearTimeout(searchTimer))
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

      <!-- dirs mode has no clickable file entries, and a directory entry click
           navigates into it rather than choosing it — so picking the folder
           currently open (including the root, reachable no other way) needs
           its own control. -->
      <Button
        v-if="mode === 'dirs'"
        type="button"
        variant="outline"
        size="sm"
        data-testid="choose-current-dir"
        @click="chooseCurrent"
      >
        选择此目录
      </Button>

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
            variant="outline"
            :disabled="busy || !selected.size"
            @click="openMove"
          >
            移动到…
          </Button>
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

    <!-- v-if="canMutate", not just "reachable only from the manage toolbar":
         FileManagerDialog's own dirs-mode picker is itself a FileManager, so
         an unconditional render here would have every dirs-mode instance
         spawn another move dialog of its own — an infinite tree of nested
         dialogs. Gating on canMutate stops the recursion one level down,
         where mode is 'dirs' and canMutate is false. Overlays start closed —
         SSR emits no teleported content. -->
    <template v-if="canMutate">
      <ConfirmDialog
        :open="confirmingDelete"
        :title="deleteTitle"
        :description="deleteDescription"
        confirm-label="删除"
        cancel-label="取消"
        @update:open="(o: boolean) => (confirmingDelete = o)"
        @confirm="performDelete"
      />
      <FileManagerDialog
        v-model:open="moveOpen"
        :base-path="basePath"
        :csrf-token="csrfToken"
        mode="dirs"
        title="移动到…"
        @select="performMove"
      />
    </template>
  </div>
</template>
