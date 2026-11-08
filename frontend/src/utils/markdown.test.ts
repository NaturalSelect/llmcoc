// @vitest-environment jsdom
import { describe, expect, test } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  test('渲染基础 Markdown，空输入返回空串', () => {
    expect(renderMarkdown('**粗体**')).toContain('<strong>粗体</strong>')
    expect(renderMarkdown('')).toBe('')
    expect(renderMarkdown(null)).toBe('')
  })

  test('链接在新标签打开且不带 opener', () => {
    const html = renderMarkdown('[文档](https://example.com/a)')
    expect(html).toContain('href="https://example.com/a"')
    expect(html).toContain('target="_blank"')
    expect(html).toContain('rel="noopener noreferrer"')
  })

  test('脚本和事件属性被清洗掉', () => {
    const html = renderMarkdown('<script>alert(1)</script><img src=x onerror="alert(1)">正文')
    expect(html).not.toContain('<script')
    expect(html).not.toContain('onerror')
    expect(html).toContain('正文')
  })

  test('javascript: 链接被清洗掉', () => {
    expect(renderMarkdown('[点我](javascript:alert(1))')).not.toContain('javascript:')
  })
})
