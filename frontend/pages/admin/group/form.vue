<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import FormField from '@/components/admin/FormField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import PermissionGrid from '@/components/admin/PermissionGrid.vue'

interface MenuItem { title: string; path: string; order?: number; section?: string }
type GroupRow = { id: number; name: string; superuser: boolean; members: number; keys: number }

const props = defineProps<{
  item: GroupRow
  basePath: string
  permissions: { resource: string; access: boolean; modify: boolean }[]
  stale?: string[]
  errors?: Record<string, string>
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
}>()

const editing = props.item.id > 0
</script>

<template>
  <AdminShell
    :menu="adminMenu"
    :user="adminUser"
    :mount="adminMount"
    :current-path="currentPath"
    :flash="flash"
    :crumb="editing ? '编辑' : '新建'"
  >
    <PageHeader :title="editing ? '编辑分组' : '新建分组'" />
    <Card>
      <CardContent class="pt-6">
        <form
          :action="editing ? `${basePath}/${item.id}` : basePath"
          method="post"
          class="space-y-4"
        >
          <FormField name="name" label="分组名" :error="errors?.name">
            <Input id="name" name="name" :model-value="item.name" :aria-invalid="!!errors?.name" />
          </FormField>

          <FormField name="superuser" label="超级管理员" :error="errors?.superuser">
            <label class="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                name="superuser"
                value="1"
                :checked="item.superuser"
                class="size-4 accent-primary"
              >
              绕过所有权限检查
            </label>
          </FormField>

          <FormField name="permissions" label="权限">
            <PermissionGrid
              :rows="permissions"
              :stale="stale"
              :superuser="item.superuser"
            />
          </FormField>

          <div class="flex gap-2">
            <Button type="submit">保存</Button>
            <Button as="a" :href="basePath" variant="outline">取消</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  </AdminShell>
</template>
