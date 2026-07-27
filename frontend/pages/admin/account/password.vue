<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import FormField from '@/components/admin/FormField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface MenuItem { title: string; path: string; order?: number; section?: string }

defineProps<{
  basePath: string
  errors?: Record<string, string>
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    crumb="修改密码"
  >
    <PageHeader title="修改密码" description="修改你自己的登录密码。" />
    <Card class="max-w-lg">
      <CardContent class="pt-6">
        <form :action="basePath" method="post" class="space-y-4">
          <FormField name="current" label="当前密码" :error="errors?.current">
            <Input id="current" name="current" type="password" :aria-invalid="!!errors?.current" />
          </FormField>
          <FormField name="password" label="新密码" :error="errors?.password">
            <Input id="password" name="password" type="password" :aria-invalid="!!errors?.password" />
            <p class="text-sm text-muted-foreground">8–72 个字符。</p>
          </FormField>
          <FormField name="confirm" label="确认新密码" :error="errors?.confirm">
            <Input id="confirm" name="confirm" type="password" :aria-invalid="!!errors?.confirm" />
          </FormField>
          <div class="flex gap-2">
            <Button type="submit">保存</Button>
            <Button as="a" :href="adminMount || '/admin'" variant="outline">取消</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  </AdminShell>
</template>
