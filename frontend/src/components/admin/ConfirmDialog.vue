<script setup lang="ts">
// The only dialog in the conventions: destructive confirmation. Two ways to
// answer "confirmed", chosen by whether `action` is given:
//  - Most callers (the three list pages) pass `action`: the confirm button
//    submits a plain form POST, so the server stays the single source of
//    truth — no client-side mutation.
//  - FileManager's delete is a JSON mutation through fetch, not a form post
//    (its endpoint decodes a JSON body, not form fields), so it omits
//    `action` and listens for `confirm` instead, running the mutation itself.
// Dialogs start closed; SSR emits no teleported content, and
// server/ssr_fixture_test.go asserts none renders.
import CsrfField from '@/components/admin/CsrfField.vue'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

defineProps<{
  open: boolean
  title: string
  description?: string
  action?: string
  confirmLabel?: string
  cancelLabel?: string
  csrfToken?: string
}>()
const emit = defineEmits<{ 'update:open': [value: boolean]; confirm: [] }>()
</script>

<template>
  <Dialog :open="open" @update:open="(o) => emit('update:open', o)">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>{{ description ?? '此操作不可撤销。' }}</DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" @click="emit('update:open', false)">{{ cancelLabel ?? '取消' }}</Button>
        <form v-if="action" :action="action" method="post">
          <CsrfField :token="csrfToken" />
          <Button type="submit" variant="destructive">{{ confirmLabel ?? '删除' }}</Button>
        </form>
        <Button v-else type="button" variant="destructive" @click="emit('confirm')">
          {{ confirmLabel ?? '删除' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
