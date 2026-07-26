<script setup lang="ts">
// Admin login. A plain HTML form POST (not an Inertia visit): the server
// authenticates, sets the session cookie, and 302-redirects to the dashboard —
// so this page needs no client-side auth logic. `loginPath` and `error` are
// provided by the admin module's handlers.
//
// `error` is the form-level channel (bad credentials). Per-field messages use
// the `errors` prop instead; the two coexist and login only needs this one.
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

defineProps<{
  loginPath?: string
  error?: string
}>()
</script>

<template>
  <div class="min-h-screen flex items-center justify-center bg-muted/40">
    <Card class="w-80">
      <CardHeader>
        <CardTitle>Admin sign in</CardTitle>
      </CardHeader>
      <CardContent>
        <form :action="loginPath || '/admin/login'" method="post" class="space-y-4">
          <Alert v-if="error" variant="destructive">
            <AlertDescription>{{ error }}</AlertDescription>
          </Alert>
          <div class="space-y-1.5">
            <Label for="username">Username</Label>
            <Input id="username" name="username" autocomplete="username" required />
          </div>
          <div class="space-y-1.5">
            <Label for="password">Password</Label>
            <Input
              id="password"
              name="password"
              type="password"
              autocomplete="current-password"
              required
            />
          </div>
          <Button type="submit" class="w-full">Sign in</Button>
        </form>
      </CardContent>
    </Card>
  </div>
</template>
