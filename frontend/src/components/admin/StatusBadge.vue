<script setup lang="ts">
// One StatusMeta, rendered.
//
// It exists because the three-line form it replaces has to name the meta object three
// times — once for :variant, once for :class, once for the label — and the queue
// screens do that for eight different badges. Three chances per badge to pass a
// slightly different argument, and the failure is a mismatched colour rather than an
// error; the cron list had six calls in one table cell.
//
// It takes the StatusMeta rather than a raw status on purpose: the mapping from a
// status to a meta is lib/task-status.ts's job and there are four of them
// (taskStatusMeta, attemptOutcomeMeta, scheduleStatusMeta, and taskStatusMeta with
// attempts). A component that picked the mapper itself would have to know which.
import { Badge } from '@/components/ui/badge'
import type { StatusMeta } from '@/lib/task-status'

defineProps<{ meta: StatusMeta }>()
</script>

<template>
  <Badge :variant="meta.variant" :class="meta.class">{{ meta.label }}</Badge>
</template>
