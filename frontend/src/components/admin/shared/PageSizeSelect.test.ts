// @vitest-environment jsdom
import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import PageSizeSelect from './PageSizeSelect.vue'

describe('PageSizeSelect', () => {
  test('渲染 10 / 20 / 50 三个选项，当前值被选中', () => {
    const wrapper = mount(PageSizeSelect, { props: { modelValue: 20 } })
    const options = wrapper.findAll('option').map((o) => o.text())
    expect(options).toStrictEqual(['10', '20', '50'])
    expect((wrapper.find('select').element as HTMLSelectElement).value).toBe('20')
  })

  test('选择新值时发出数字而不是字符串', async () => {
    const wrapper = mount(PageSizeSelect, { props: { modelValue: 20 } })
    await wrapper.find('select').setValue('50')
    const emitted = wrapper.emitted('update:modelValue')
    expect(emitted).toStrictEqual([[50]])
  })

  test('label 与 select 通过同一个 id 关联', () => {
    const wrapper = mount(PageSizeSelect, { props: { modelValue: 10 } })
    const id = wrapper.find('select').attributes('id')
    expect(id).toBeTruthy()
    expect(wrapper.find('label').attributes('for')).toBe(id)
  })
})
