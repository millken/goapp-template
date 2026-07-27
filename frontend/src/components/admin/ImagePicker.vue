<script setup lang="ts">
// A control for FormField's slot: a preview, a button that opens the library,
// and a hidden input carrying the chosen path so a plain form POST submits it.
//
// canBrowse is the caller's filemanager.access, delivered as a page prop. When
// it is false this degrades to a text input: the picker's endpoints would 403,
// and a button guaranteed to fail is worse than a field the user can still type
// into.
import { computed, ref, watch } from 'vue'
import FileManagerDialog from '@/components/admin/FileManagerDialog.vue'
import type { FmEntry } from '@/components/admin/FileManager.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

const props = withDefaults(
  defineProps<{
    name: string
    modelValue?: string
    basePath?: string
    urlPrefix?: string
    csrfToken?: string
    canBrowse?: boolean
  }>(),
  { modelValue: '', basePath: '/admin/filemanager', urlPrefix: '/uploads', canBrowse: false },
)

const emit = defineEmits<{ (e: 'update:modelValue', v: string): void }>()

const open = ref(false)

// A local ref kept in sync by a watcher, not a computed get/set: this is
// mounted one-way as `:model-value="item.avatar"` from an Inertia page prop
// with nothing listening for `update:modelValue`, so a computed setter would
// emit into a void and picking an image would never change what's on screen.
// The component has to hold its own state (so the picker's own emit still
// works) *and* resync when the parent hands it a new value (so a reused
// instance or a post-reset repopulation isn't left showing stale data).
const value = ref(props.modelValue)
watch(
  () => props.modelValue,
  (v) => {
    value.value = v
  },
)

const preview = computed(() => {
  if (!value.value) return ''
  const prefix = props.urlPrefix.replace(/\/+$/, '')
  const path = value.value.replace(/^\/+/, '')
  return `${prefix}/${path}`
})

function set(v: string) {
  value.value = v
  emit('update:modelValue', v)
}
</script>

<template>
  <div class="space-y-2">
    <div v-if="preview" class="flex items-center gap-3">
      <img :src="preview" :alt="value" class="size-16 rounded border object-cover" />
      <span class="truncate text-xs text-muted-foreground">{{ value }}</span>
    </div>

    <template v-if="canBrowse">
      <input type="hidden" :name="name" :value="value" />
      <div class="flex gap-2">
        <Button type="button" variant="outline" @click="open = true">选择图片</Button>
        <Button v-if="value" type="button" variant="ghost" @click="set('')">清除</Button>
      </div>
      <FileManagerDialog
        v-model:open="open"
        :base-path="basePath"
        :url-prefix="urlPrefix"
        :csrf-token="csrfToken"
        title="选择图片"
        @select="(entry: FmEntry) => set(entry.path)"
      />
    </template>

    <template v-else>
      <Input
        type="text"
        :name="name"
        :model-value="value"
        placeholder="图片路径，例如 photos/a.png"
        @update:model-value="(v: string | number) => set(String(v))"
      />
    </template>
  </div>
</template>
