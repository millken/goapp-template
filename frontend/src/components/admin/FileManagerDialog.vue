<script setup lang="ts">
// The picker shell: a Dialog around FileManager in pick (or dirs) mode. It owns
// nothing but open state — the listing, the mutations and the errors all stay
// in FileManager, so the page and the modal cannot drift apart.
import FileManager, { type FmEntry } from '@/components/admin/FileManager.vue'
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

withDefaults(
  defineProps<{
    open: boolean
    // Required, not defaulted: a hardcoded '/admin/filemanager' guess broke
    // silently the moment admin.Config.Mount was anything else. Every caller
    // now derives this from the adminMount the server actually reported (see
    // ImagePicker's own basePath computed) rather than repeating a literal.
    basePath: string
    csrfToken?: string
    mode?: 'pick' | 'dirs'
    title?: string
  }>(),
  {
    mode: 'pick',
    title: '选择文件',
  },
)

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'select', entry: FmEntry): void
}>()

function choose(entry: FmEntry) {
  emit('select', entry)
  emit('update:open', false)
}
</script>

<template>
  <Dialog :open="open" @update:open="(v: boolean) => emit('update:open', v)">
    <DialogContent class="max-w-3xl">
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>点击文件夹进入，点击文件选中。</DialogDescription>
      </DialogHeader>
      <FileManager
        :base-path="basePath"
        :csrf-token="csrfToken"
        :mode="mode"
        @select="choose"
      />
    </DialogContent>
  </Dialog>
</template>
