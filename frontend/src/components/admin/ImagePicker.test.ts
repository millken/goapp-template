// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick } from 'vue'
import ImagePicker from './ImagePicker.vue'

function mount(props: Record<string, unknown> = {}) {
  const el = document.createElement('div')
  document.body.appendChild(el)
  createApp({ render: () => h(ImagePicker, { name: 'avatar', ...props } as any) }).mount(el)
  return el
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('ImagePicker', () => {
  it('always submits its value through a named input', () => {
    const el = mount({ modelValue: 'a/b.png' })
    const input = el.querySelector('input[name="avatar"]') as HTMLInputElement
    expect(input).toBeTruthy()
    expect(input.value).toBe('a/b.png')
  })

  it('offers the picker when the caller may browse', () => {
    const el = mount({ canBrowse: true })
    expect(el.textContent).toContain('选择图片')
    expect(el.querySelector('input[type="text"][name="avatar"]')).toBeNull()
  })

  it('degrades to a text field when the caller may not browse', async () => {
    // A user with user.modify but no filemanager.access would get a 403 from
    // the picker's endpoints, so the button must not be offered at all.
    const el = mount({ canBrowse: false, modelValue: 'a/b.png' })
    await nextTick()
    expect(el.textContent).not.toContain('选择图片')
    const text = el.querySelector('input[type="text"][name="avatar"]') as HTMLInputElement
    expect(text).toBeTruthy()
    expect(text.value).toBe('a/b.png')
  })

  it('clears the value', async () => {
    const el = mount({ canBrowse: true, modelValue: 'a/b.png' })
    const clear = [...el.querySelectorAll('button')].find((b) => b.textContent?.includes('清除'))
    expect(clear).toBeTruthy()
    clear!.click()
    await nextTick()
    const input = el.querySelector('input[name="avatar"]') as HTMLInputElement
    expect(input.value).toBe('')
  })
})
