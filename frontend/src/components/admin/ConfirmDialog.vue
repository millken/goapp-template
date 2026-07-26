<script setup lang="ts">
// The only dialog in the conventions: destructive confirmation. The confirm
// button submits a plain form POST to `action`, so the server stays the single
// source of truth — no client-side mutation. Dialogs start closed; SSR emits no
// teleported content, and server/ssr_fixture_test.go asserts none renders.
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'

defineProps<{
  open: boolean
  title: string
  description?: string
  action: string
  confirmLabel?: string
}>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()
</script>

<template>
  <Dialog :open="open" @update:open="(o) => emit('update:open', o)">
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{{ title }}</DialogTitle>
        <DialogDescription>{{ description ?? 'This cannot be undone.' }}</DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" @click="emit('update:open', false)">Cancel</Button>
        <form :action="action" method="post">
          <Button type="submit" variant="destructive">{{ confirmLabel ?? 'Delete' }}</Button>
        </form>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
