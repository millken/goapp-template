<script setup lang="ts">
// Admin login. A plain HTML form POST (not an Inertia visit): the server
// authenticates, sets the session cookie, and 302-redirects to the dashboard —
// so this page needs no client-side auth logic. `loginPath` and `error` are
// provided by the admin module's handlers.
//
// Two channels reach this page, and it has to render both. `error` is what the
// handler sets when a sign-in attempt fails. `flash` is what the *admin area*
// staged before bouncing someone here — a disabled account, say. This is the one
// admin page outside AdminShell, which renders flash for every other page, so
// forgetting it here means the reason is delivered and then silently discarded.
import CsrfField from '@/components/admin/CsrfField.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

defineProps<{
  loginPath?: string
  error?: string
  flash?: Record<string, string>
  csrfToken?: string
}>()
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-muted/40">
    <Card class="w-80">
      <CardHeader>
        <CardTitle>后台登录</CardTitle>
      </CardHeader>
      <CardContent>
        <form :action="loginPath || '/admin/login'" method="post" class="space-y-4">
          <CsrfField :token="csrfToken" />
          <Alert
            v-for="(message, kind) in flash || {}"
            :key="kind"
            :variant="kind === 'error' ? 'destructive' : 'default'"
          >
            <AlertDescription>{{ message }}</AlertDescription>
          </Alert>
          <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
          </Alert>
          <div class="space-y-1.5">
            <Label for="username">用户名</Label>
            <Input id="username" name="username" autocomplete="username" required />
          </div>
          <div class="space-y-1.5">
            <Label for="password">密码</Label>
            <Input
              id="password"
              name="password"
              type="password"
              autocomplete="current-password"
              required
            />
          </div>
          <Button type="submit" class="w-full">登录</Button>
        </form>
      </CardContent>
    </Card>
  </div>
</template>
