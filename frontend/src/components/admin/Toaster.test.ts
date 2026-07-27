// @vitest-environment happy-dom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, h, nextTick } from 'vue'

// vue-sonner is mocked rather than driven: whether sonner paints a toast is
// sonner's problem, and asserting its DOM here would test the library. What is
// ours is the contract with it — which kind becomes which call, and with what
// duration — so that is what these assert.
const success = vi.fn()
const error = vi.fn()
vi.mock('vue-sonner', () => ({
  toast: {
    success: (...args: unknown[]) => success(...args),
    error: (...args: unknown[]) => error(...args),
  },
  Toaster: { name: 'Toaster', render: () => null },
}))
vi.mock('@/components/ui/sonner', () => ({
  Toaster: { name: 'Toaster', render: () => null },
}))

import Toaster from './Toaster.vue'

function mount(props: Record<string, unknown>) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(Toaster, props as any) }).mount(el)
  return el
}

describe('Toaster', () => {
  beforeEach(() => {
    success.mockClear()
    error.mockClear()
  })

  it('raises a success as a self-dismissing toast', async () => {
    mount({ messages: { success: '用户已创建' }, duration: 4000 })
    await nextTick()
    expect(success).toHaveBeenCalledTimes(1)
    const [message, opts] = success.mock.calls[0] as [string, { duration: number }]
    expect(message).toBe('用户已创建')
    expect(opts.duration).toBe(4000)
  })

  // A missed error reads as nothing having happened, so an error toast waits for
  // the reader instead of timing out — and therefore needs a way to be closed.
  it('raises an error that does not time out, and can be dismissed', async () => {
    mount({ messages: { error: '同步失败' } })
    await nextTick()
    expect(error).toHaveBeenCalledTimes(1)
    const [message, opts] = error.mock.calls[0] as [
      string,
      { duration: number; closeButton: boolean },
    ]
    expect(message).toBe('同步失败')
    expect(opts.duration).toBe(Number.POSITIVE_INFINITY)
    expect(opts.closeButton).toBe(true)
  })

  it('raises nothing when there is nothing to say', async () => {
    mount({})
    await nextTick()
    expect(success).not.toHaveBeenCalled()
    expect(error).not.toHaveBeenCalled()
  })
})
