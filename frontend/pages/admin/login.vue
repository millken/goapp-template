<script setup lang="ts">
// Admin login. A plain HTML form POST (not an Inertia visit): the server
// authenticates, sets the session cookie, and 302-redirects to the dashboard —
// so this page needs no client-side auth logic. `loginPath` and `error` are
// provided by the admin module's handlers.
defineProps<{
  loginPath?: string
  error?: string
}>()
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-gray-50">
    <form
      :action="loginPath || '/admin/login'"
      method="post"
      class="w-80 bg-white p-6 rounded shadow space-y-4"
    >
      <h1 class="text-xl font-semibold text-gray-900">Admin sign in</h1>
      <p v-if="error" class="text-sm text-red-600">{{ error }}</p>
      <div>
        <label class="block text-sm text-gray-600 mb-1">Username</label>
        <input
          name="username"
          type="text"
          autocomplete="username"
          class="w-full border rounded px-3 py-2"
          required
        />
      </div>
      <div>
        <label class="block text-sm text-gray-600 mb-1">Password</label>
        <input
          name="password"
          type="password"
          autocomplete="current-password"
          class="w-full border rounded px-3 py-2"
          required
        />
      </div>
      <button type="submit" class="w-full bg-gray-900 text-white rounded px-3 py-2">
        Sign in
      </button>
    </form>
  </div>
</template>
