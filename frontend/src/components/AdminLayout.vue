<script setup lang="ts">
// Shared admin shell: sidebar nav (built from the server-provided menu) + a
// logout form, with page content in the default slot. Menu items are registered
// by admin resource modules (AddMenuItem) and injected as the `adminMenu` prop
// by the admin auth middleware.
interface MenuItem {
  title: string
  path: string
  order?: number
}

defineProps<{
  menu?: MenuItem[]
  user?: unknown
  mount?: string
  loginPath?: string
}>()
</script>

<template>
  <div class="min-h-screen flex bg-gray-50">
    <aside class="w-56 bg-gray-900 text-gray-100 p-4 flex flex-col">
      <div class="text-lg font-semibold mb-6">Admin</div>
      <nav class="flex-1 space-y-1">
        <a :href="mount || '/admin'" class="block px-3 py-2 rounded hover:bg-gray-800">Dashboard</a>
        <a
          v-for="item in menu || []"
          :key="item.path"
          :href="item.path"
          class="block px-3 py-2 rounded hover:bg-gray-800"
        >{{ item.title }}</a>
      </nav>
      <form :action="(mount || '/admin') + '/logout'" method="post" class="mt-4">
        <button
          type="submit"
          class="w-full px-3 py-2 rounded bg-gray-800 hover:bg-gray-700 text-sm"
        >Log out</button>
      </form>
    </aside>
    <main class="flex-1 p-8">
      <slot />
    </main>
  </div>
</template>
