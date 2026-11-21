// @vitest-environment jsdom
import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, test } from 'vitest'
import { nextTick } from 'vue'
import type { InventoryItem } from '@/api/characterTypes'
import ItemDetailTip, { clearPinnedItemDetail } from './ItemDetailTip.vue'

const wrappers: VueWrapper[] = []

function mountTip(item: InventoryItem) {
  const w = mount(ItemDetailTip, { props: { item }, attachTo: document.body })
  wrappers.push(w)
  return { wrapper: w, btn: w.get('button').element as HTMLButtonElement }
}

// NOTE: 模拟真实浏览器的鼠标点击序列，覆盖 pointerdown -> focus -> click 的交互顺序
async function clickLikeMouse(btn: HTMLButtonElement) {
  btn.dispatchEvent(new Event('pointerdown', { bubbles: true }))
  btn.dispatchEvent(new FocusEvent('focus'))
  btn.click()
  await nextTick()
}

const tips = () => [...document.body.querySelectorAll('[role="tooltip"]')]
const tip = () => document.body.querySelector('[role="tooltip"]')

afterEach(() => {
  while (wrappers.length) wrappers.pop()?.unmount()
  clearPinnedItemDetail()
})

describe('ItemDetailTip', () => {
  test('默认只显示名称，悬停显示浮窗，移开后收起', async () => {
    const { btn } = mountTip({ name: '左轮手枪', desc: '六发弹巢', count: 2, type: 'weapon' })
    expect(btn.textContent?.trim()).toBe('左轮手枪')
    expect(btn.getAttribute('aria-pressed')).toBe('false')
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

  test('点击后固定，移开、失焦和点击外部均保留；再次点击同一装备立即取消固定', async () => {
    const { btn } = mountTip({ name: '绷带', count: 1, type: 'misc' })
    btn.dispatchEvent(new MouseEvent('mouseenter'))
    await clickLikeMouse(btn)
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    expect(tip()?.textContent).toContain('暂无描述')

    btn.dispatchEvent(new MouseEvent('mouseleave'))
    btn.dispatchEvent(new FocusEvent('blur'))
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await nextTick()
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    expect(tip()?.textContent).toContain('暂无描述')

    btn.dispatchEvent(new MouseEvent('mouseenter'))
    await clickLikeMouse(btn)
    expect(btn.getAttribute('aria-pressed')).toBe('false')
    expect(tip()).toBeNull()
  })

  test('固定一项时悬停其他装备不会弹出重叠浮窗；点击另一装备自动切换固定', async () => {
    const gun = mountTip({ name: '左轮手枪', desc: '.38口径', count: 1, type: 'weapon' })
    const lamp = mountTip({ name: '手电筒', desc: '黄铜', count: 1, type: 'equipment' })

    await clickLikeMouse(gun.btn)
    expect(gun.btn.getAttribute('aria-pressed')).toBe('true')
    expect(tips()).toHaveLength(1)
    expect(tip()?.textContent).toContain('.38口径')

    lamp.btn.dispatchEvent(new MouseEvent('mouseenter'))
    await nextTick()
    expect(tips()).toHaveLength(1)
    expect(tip()?.textContent).toContain('.38口径')

    await clickLikeMouse(lamp.btn)
    expect(gun.btn.getAttribute('aria-pressed')).toBe('false')
    expect(lamp.btn.getAttribute('aria-pressed')).toBe('true')
    expect(tips()).toHaveLength(1)
    expect(tip()?.textContent).toContain('黄铜')

    await clickLikeMouse(lamp.btn)
    expect(lamp.btn.getAttribute('aria-pressed')).toBe('false')
    expect(tip()).toBeNull()

    gun.btn.dispatchEvent(new MouseEvent('mouseenter'))
    await nextTick()
    expect(tip()?.textContent).toContain('.38口径')
  })

  test('组件卸载时取消固定，重新挂载不会残留', async () => {
    const first = mountTip({ name: '左轮手枪', desc: '.38口径', count: 1, type: 'weapon' })
    await clickLikeMouse(first.btn)
    expect(tip()).not.toBeNull()

    first.wrapper.unmount()
    await nextTick()
    expect(tip()).toBeNull()

    const second = mountTip({ name: '左轮手枪', desc: '.38口径', count: 1, type: 'weapon' })
    await nextTick()
    expect(second.btn.getAttribute('aria-pressed')).toBe('false')
    expect(tip()).toBeNull()
  })

  test('同节点换为另一物品时取消固定，同物品仅刷新数量时保留固定', async () => {
    const { wrapper, btn } = mountTip({ name: '绷带', desc: '止血', count: 2, type: 'misc' })
    await clickLikeMouse(btn)
    expect(btn.getAttribute('aria-pressed')).toBe('true')

    await wrapper.setProps({ item: { name: '绷带', desc: '止血', count: 1, type: 'misc' } })
    expect(btn.getAttribute('aria-pressed')).toBe('true')
    expect(tip()?.textContent).toContain('1')

    await wrapper.setProps({ item: { name: '手电筒', desc: '黄铜', count: 1, type: 'equipment' } })
    expect(btn.getAttribute('aria-pressed')).toBe('false')
    expect(tip()).toBeNull()
  })

  test('键盘聚焦显示、失焦收起；Esc 只收起浮窗，不继续冒泡到弹窗的关闭监听', async () => {
    const { btn } = mountTip({ name: '绷带', count: 1, type: 'misc' })
    btn.dispatchEvent(new FocusEvent('focus'))
    await nextTick()
    expect(tip()?.textContent).toContain('暂无描述')

    btn.dispatchEvent(new FocusEvent('blur'))
    await nextTick()
    expect(tip()).toBeNull()

    await clickLikeMouse(btn)
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
