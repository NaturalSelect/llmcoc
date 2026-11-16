// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import type { OnlineModule } from '@/api/adminTypesOnline'
import OnlineModuleList from './OnlineModuleList.vue'

const item = (over: Partial<OnlineModule> = {}): OnlineModule =>
  ({
    id: 1,
    title: '雨夜档案',
    author: '某作者',
    status: 'extracted',
    scenario_id: null,
    stage: '',
    error: '',
    meta: {},
    updated_at: '2026-11-14T09:00:00Z',
    ...over,
  }) as OnlineModule

const base = { items: [item()], page: 2, pageSize: 20, total: 45, totalPages: 3 }

describe('OnlineModuleList', () => {
  test('翻页条在列表下方，头部只留计数、每页条数和刷新', () => {
    const wrapper = mount(OnlineModuleList, { props: base, attachTo: document.body })
    const header = wrapper.text()
    expect(header).toContain('共 45 个')
    expect(header).toContain('2 / 3')
    // NOTE: 头部不再有文字按钮的上一页/下一页，也不重复「第 p / t 页」
    expect(wrapper.findAll('button').some((b) => b.text() === '上一页' || b.text() === '下一页')).toBe(false)
    expect(header).not.toContain('第 2 / 3 页')
    expect(wrapper.find('select').exists()).toBe(true)
    expect(wrapper.findAll('button').some((b) => b.text() === '刷新')).toBe(true)

    const list = wrapper.find('ul').element
    const next = wrapper.find('button[aria-label="下一页"]').element
    expect(list.compareDocumentPosition(next) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    wrapper.unmount()
  })

  test('点下方的上一页 / 下一页发出目标页码', async () => {
    const wrapper = mount(OnlineModuleList, { props: base })
    await wrapper.find('button[aria-label="下一页"]').trigger('click')
    await wrapper.find('button[aria-label="上一页"]').trigger('click')
    expect(wrapper.emitted('goto')).toStrictEqual([[3], [1]])
  })

  test('输入页码跳转，超出范围夹到首尾页，非数字被忽略', async () => {
    const wrapper = mount(OnlineModuleList, { props: base })
    const input = wrapper.find('input[aria-label="跳转到页码"]')
    const submit = async (text: string) => {
      await input.setValue(text)
      await wrapper.find('form').trigger('submit')
    }
    await submit('3')
    await submit('99')
    await submit('0')
    await submit('abc')
    // NOTE: 目标页等于当前页（第 2 页）时不发事件
    await submit('2')
    expect(wrapper.emitted('goto')).toStrictEqual([[3], [3], [1]])
  })

  test('只有一页时不显示翻页条', () => {
    const wrapper = mount(OnlineModuleList, { props: { ...base, page: 1, total: 1, totalPages: 1 } })
    expect(wrapper.find('button[aria-label="下一页"]').exists()).toBe(false)
  })

  test('每页条数变化发出数字', async () => {
    const wrapper = mount(OnlineModuleList, { props: base })
    await wrapper.find('select').setValue('50')
    expect(wrapper.emitted('pageSize')).toStrictEqual([[50]])
  })
})
