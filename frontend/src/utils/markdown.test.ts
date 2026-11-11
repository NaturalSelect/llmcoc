// @vitest-environment jsdom
import { describe, expect, test } from 'vitest'
import { renderMarkdown, renderProse } from './markdown'

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

describe('renderProse', () => {
  test('保留小说排版：段落、斜体、粗体、引用块、分隔线', () => {
    const html = renderProse('雨落下。\n\n*有人在低语*，**别回头**。\n\n> 致亲爱的玛莎\n\n---\n\n天亮了。')
    expect(html).toContain('<p>雨落下。</p>')
    expect(html).toContain('<em>有人在低语</em>')
    expect(html).toContain('<strong>别回头</strong>')
    expect(html).toContain('<blockquote>')
    expect(html).toContain('<hr>')
  })

  test('旧的纯文本正文按单个换行分行', () => {
    expect(renderProse('第一行\n第二行')).toContain('第一行<br>第二行')
  })

  test('标题、链接、图片和脚本只留文字或被去掉', () => {
    const html = renderProse('# 标题\n\n[点我](https://example.com) ![图](x.png)<script>alert(1)</script><b onclick="x">字</b>')
    expect(html).not.toMatch(/<(h1|a|img|script|b)\b/)
    expect(html).not.toContain('onclick')
    expect(html).toContain('标题')
    expect(html).toContain('点我')
    expect(html).toContain('字')
  })
})
