<script setup lang="ts">
// Shared admin shell: sidebar nav (built from the server-provided menu) + a
// logout form, with page content in the default slot. Menu items are registered
// by admin resource modules (Registrar.Menu) and injected as the `adminMenu`
// prop, already filtered to what the signed-in user may reach.
//
// Navigation stays plain <a href>: Button renders an anchor via `as`, and the
// PJAX layer intercepts those through document-level delegation, so there is no
// router integration to wire.
import { computed } from 'vue'
import { LogOut } from 'lucide-vue-next'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'

interface MenuItem {
  title: string
  path: string
  order?: number
}

// `user` and `loginPath` are unused here but declared so Vue treats them as
// props rather than leaking them onto the root element as attributes.
const props = defineProps<{
  menu?: MenuItem[]
  user?: unknown
  mount?: string
  loginPath?: string
  flash?: Record<string, string>
}>()

const base = computed(() => props.mount || '/admin')

// One-shot messages staged by the server before a redirect (sess.Flash), keyed
// by kind. The session middleware consumes them, so they vanish on the next
// navigation — no dismiss button needed.
const flashVariant = (kind: string) =>
  kind === 'error' ? 'destructive' : kind === 'success' ? 'success' : 'default'
</script>

<template>
  <div class="min-h-screen flex bg-muted/40">
    <aside class="w-56 border-r bg-background p-4 flex flex-col">
      <div class="text-lg font-semibold mb-4">Admin</div>
      <Separator />
      <nav class="flex-1 space-y-1 py-4">
        <Button as="a" :href="base" variant="ghost" class="w-full justify-start">
          Dashboard
        </Button>
        <Button
          v-for="item in menu || []"
          :key="item.path"
          as="a"
          :href="item.path"
          variant="ghost"
          class="w-full justify-start"
        >{{ item.title }}</Button>
      </nav>
      <form :action="`${base}/logout`" method="post">
        <Button type="submit" variant="outline" size="sm" class="w-full">
          <LogOut />
          Log out
        </Button>
      </form>
    </aside>

    <main class="flex-1 p-8">
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
</template>
