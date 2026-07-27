// @vitest-environment happy-dom
import { afterEach, describe, expect, it } from 'vitest'
import { createApp, h, nextTick, ref } from 'vue'
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
    const input = el.querySelector('input[name="avatar"]') as HTMLInputElement
    // Pin the picker branch's initial binding: without this, a regression
    // that broke only the initial value (leaving the clear button's own
    // wiring intact) would still show 'a/b.png' as an empty string simply
    // because nothing before this line ever asserted it started non-empty.
    expect(input.value).toBe('a/b.png')
    const clear = [...el.querySelectorAll('button')].find((b) => b.textContent?.includes('清除'))
    expect(clear).toBeTruthy()
    clear!.click()
    await nextTick()
    expect(input.value).toBe('')
  })

  it('follows a new modelValue assigned by the parent after mount', async () => {
    // Mirrors how this is actually consumed: `<ImagePicker :model-value="item.avatar">`
    // where `item` is an Inertia page prop with no listener writing back through
    // `update:modelValue`. If the parent reassigns modelValue (reusing the
    // instance across records, repopulating after a reset), the preview and the
    // named input must follow, not keep showing what was there at mount time.
    const current = ref('a/b.png')
    const el = document.createElement('div')
    document.body.appendChild(el)
    createApp({
      render: () => h(ImagePicker, { name: 'avatar', canBrowse: true, modelValue: current.value }),
    }).mount(el)

    const input = () => el.querySelector('input[name="avatar"]') as HTMLInputElement
    expect(input().value).toBe('a/b.png')
    expect(el.querySelector('img')?.getAttribute('src')).toBe('/uploads/a/b.png')

    current.value = 'c/d.png'
    await nextTick()

    expect(input().value).toBe('c/d.png')
    expect(el.querySelector('img')?.getAttribute('src')).toBe('/uploads/c/d.png')
  })
})
