<script setup lang="ts">
import AdminShell from '@/components/admin/AdminShell.vue'
import CsrfField from '@/components/admin/CsrfField.vue'
import FormField from '@/components/admin/FormField.vue'
import PageHeader from '@/components/admin/PageHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'

interface MenuItem { title: string; path: string; order?: number; section?: string }
type UserRow = { id: number; username: string; group_id: number; group: string; status: number }

const props = defineProps<{
  item: UserRow
  groups: { id: number; name: string }[]
  basePath: string
  // Set by the handler only when a submit failed validation: one message per bad
  // field. `item` carries what was typed, so the inputs repopulate on their own.
  errors?: Record<string, string>
  adminMenu?: MenuItem[]
  adminUser?: { id?: number; username?: string }
  adminMount?: string
  loginPath?: string
  currentPath?: string
  flash?: Record<string, string>
  csrfToken?: string
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
    :csrf-token="csrfToken"
    :crumb="editing ? '编辑' : '新建'"
  >
    <PageHeader :title="editing ? '编辑用户' : '新建用户'" />
    <Card class="max-w-lg">
      <CardContent class="pt-6">
        <form
          :action="editing ? `${basePath}/${item.id}` : basePath"
          method="post"
          class="space-y-4"
        >
          <CsrfField :token="csrfToken" />
          <FormField name="username" label="用户名" :error="errors?.username">
            <Input
              id="username"
              name="username"
              :model-value="item.username"
              :aria-invalid="!!errors?.username"
            />
          </FormField>

          <FormField name="group_id" label="分组" :error="errors?.group_id">
            <select
              id="group_id"
              name="group_id"
              class="h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm"
              :aria-invalid="!!errors?.group_id"
            >
              <option v-for="g in groups" :key="g.id" :value="g.id" :selected="g.id === item.group_id">
                {{ g.name }}
              </option>
            </select>
          </FormField>

          <FormField
            name="password"
            :label="editing ? '新密码' : '密码'"
            :error="errors?.password"
          >
            <Input
              id="password"
              name="password"
              type="password"
              :aria-invalid="!!errors?.password"
            />
            <p class="text-sm text-muted-foreground">
              {{ editing ? '留空表示不修改密码。' : '8–72 个字符。' }}
            </p>
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
