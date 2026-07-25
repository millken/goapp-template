<script setup lang="ts">
// Shared admin shell: sidebar nav (built from the server-provided menu) + a
// logout form, with page content in the default slot. Menu items are registered
// by admin resource modules (AddMenuItem) and injected as the `adminMenu` prop
// by the admin auth middleware.
import { computed } from 'vue'

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
const flashClass = (kind: string) =>
  ({
    success: 'bg-green-50 text-green-800 border-green-200',
    error: 'bg-red-50 text-red-800 border-red-200',
  })[kind] ?? 'bg-gray-50 text-gray-700 border-gray-200'
</script>

<template>
  <div class="min-h-screen flex bg-gray-50">
    <aside class="w-56 bg-gray-900 text-gray-100 p-4 flex flex-col">
      <div class="text-lg font-semibold mb-6">Admin</div>
      <nav class="flex-1 space-y-1">
        <a :href="base" class="block px-3 py-2 rounded hover:bg-gray-800">Dashboard</a>
        <a
          v-for="item in menu || []"
          :key="item.path"
          :href="item.path"
          class="block px-3 py-2 rounded hover:bg-gray-800"
        >{{ item.title }}</a>
      </nav>
      <form :action="`${base}/logout`" method="post" class="mt-4">
        <button
          type="submit"
          class="w-full px-3 py-2 rounded bg-gray-800 hover:bg-gray-700 text-sm"
        >Log out</button>
      </form>
    </aside>
    <main class="flex-1 p-8">
      <div
        v-for="(message, kind) in flash || {}"
        :key="kind"
        class="mb-4 border rounded px-4 py-3 text-sm"
        :class="flashClass(kind)"
      >{{ message }}</div>
      <slot />
    </main>
  </div>
</template>
