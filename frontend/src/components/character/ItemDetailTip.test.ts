// @vitest-environment jsdom
import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, test } from 'vitest'
import { nextTick } from 'vue'
import ItemDetailTip from './ItemDetailTip.vue'

let wrapper: VueWrapper

function mountTip(item: { name: string; desc?: string; count: number; type: 'misc' | 'weapon' }) {
  wrapper = mount(ItemDetailTip, { props: { item }, attachTo: document.body })
  return wrapper.get('button').element as HTMLButtonElement
}

const tip = () => document.body.querySelector('[role="tooltip"]')

afterEach(() => wrapper?.unmount())

describe('ItemDetailTip', () => {
  test('默认只显示名称，悬停显示浮窗，移开后收起', async () => {
    const btn = mountTip({ name: '左轮手枪', desc: '六发弹巢', count: 2, type: 'weapon' })
    expect(btn.textContent?.trim()).toBe('左轮手枪')
    expect(tip()).toBeNull()

    btn.dispatchEvent(new MouseEvent('mouseenter'))
    await nextTick()
    expect(tip()?.textContent).toContain('六发弹巢')
    expect(tip()?.textContent).toContain('武器')
    expect(tip()?.textContent).toContain('2')

    btn.dispatchEvent(new MouseEvent('mouseleave'))
    await nextTick()
    expect(tip()).toBeNull()
  })

  test('点击后固定，鼠标移开仍保留；点击外部收起', async () => {
    const btn = mountTip({ name: '绷带', count: 1, type: 'misc' })
    btn.click()
    await nextTick()
    btn.dispatchEvent(new MouseEvent('mouseleave'))
    await nextTick()
    expect(tip()?.textContent).toContain('暂无描述')

    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()
    expect(tip()).toBeNull()
  })

  test('Esc 只收起浮窗，不继续冒泡到弹窗的关闭监听', async () => {
    const btn = mountTip({ name: '绷带', count: 1, type: 'misc' })
    btn.click()
    await nextTick()
    let reachedDocument = false
    const spy = () => (reachedDocument = true)
    document.addEventListener('keydown', spy)
    btn.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await nextTick()
    document.removeEventListener('keydown', spy)
    expect(tip()).toBeNull()
    expect(reachedDocument).toBe(false)
  })
})
