<script setup lang="ts">
// The hidden input every posting form needs, in one place so the field name
// lives in one place too. It must match session.CSRFFormField on the server.
//
// The PJAX layer submits a form as FormData, so this input reaches the server
// unchanged whether or not JavaScript intercepted the submit — which is why the
// token travels as a field rather than a header.
//
// An absent token renders an empty value rather than nothing: in a build with
// the session component trimmed there is no token and no middleware to check
// one, and a .vue file cannot carry a goappctl marker to make the field
// conditional.
defineProps<{ token?: string }>()
</script>

<template>
  <input type="hidden" name="_csrf" :value="token ?? ''">
</template>
