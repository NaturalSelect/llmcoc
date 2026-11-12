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

describe('renderProse 配图', () => {
  const HASH = 'ab12'.repeat(16)
  const ILL = { 1: { status: 'done' as const, url: `/api/images/${HASH}` }, 3: { status: 'failed' as const } }

  test('已完成的配图输出指向后端图片接口的 img，带说明文字', () => {
    const html = renderProse('他推开门。\n\n![门廊的灯](illust://1)\n\n灯灭了。', ILL)
    expect(html).toContain(`<img src="/api/images/${HASH}" alt="门廊的灯" loading="lazy" decoding="async">`)
    expect(html).toContain('<p>他推开门。</p>')
    expect(html).toContain('<p>灯灭了。</p>')
  })

  test('说明文字里的引号和尖括号被转义，不会逃出 alt 属性', () => {
    const html = renderProse('![a" onerror="x"<b>](illust://1)', ILL)
    expect(html).not.toContain('onerror="x"')
    expect(html).toMatch(/^<p><img src="\/api\/images\/[0-9a-f]{64}" alt="[^"]*" loading="lazy" decoding="async"><\/p>/)
  })

  test('还没画完的占位（meta 里没有这个 slot）显示「配图生成中…」，放在段落里', () => {
    expect(renderProse('![远景](illust://2)', ILL)).toBe('<p><span class="illust-pending">配图生成中…</span></p>\n')
    expect(renderProse('![远景](illust://2)')).toContain('illust-pending')
  })

  test('画失败的配图什么都不显示', () => {
    const html = renderProse('前文\n\n![失败](illust://3)\n\n后文', ILL)
    expect(html).not.toMatch(/<img|illust/)
    expect(html).toContain('前文')
    expect(html).toContain('后文')
  })

  test('done 但没有合法地址的条目不显示图片', () => {
    expect(renderProse('![x](illust://5)', { 5: { status: 'done' } })).not.toMatch(/<img|illust/)
  })

  test('格式不对的 illust 地址一律不显示', () => {
    for (const href of ['illust://abc', 'illust://-1', 'illust://1/x', 'illust://1.5', 'illust:///1']) {
      expect(renderProse(`![x](${href})`, ILL), href).not.toMatch(/<img|illust-pending/)
    }
  })

  test('markdown 写法的外链图片和站内路径都被丢掉，不会向外部发请求', () => {
    for (const href of ['https://evil.example/a.png', 'http://evil.example/a.png', '//evil.example/a.png', 'data:image/png;base64,AAAA', `/api/images/${HASH}`]) {
      const html = renderProse(`前文 ![x](${href}) 后文`, ILL)
      expect(html, href).not.toMatch(/<img|evil|data:|\/api\/images/)
      expect(html).toContain('前文')
    }
  })

  test('正文里直接写的 HTML img：只放行后端图片接口的地址，其余整个元素删掉', () => {
    const bad = ['https://evil.example/a.png', '/api/images/short', `/api/images/${HASH.toUpperCase()}`, `/api/images/${HASH}/x`, `/api/images/${HASH}?a=1`, ` /api/images/${HASH}`, '']
    for (const src of bad) {
      expect(renderProse(`<img src="${src}">`, ILL), src).not.toContain('<img')
    }
    expect(renderProse('<img alt="无地址">')).not.toContain('<img')

    const ok = renderProse(`<img src="/api/images/${HASH}" onerror="alert(1)" srcset="https://evil.example/a.png 2x" style="x:y" class="fixed inset-0">`)
    expect(ok).toContain(`<img src="/api/images/${HASH}">`)
    expect(ok).not.toMatch(/onerror|srcset|style|class|evil/)
  })

  test('class 只认占位块那一个值', () => {
    expect(renderProse('<span class="fixed inset-0 bg-bg">盖住屏幕</span>')).toBe('<p><span>盖住屏幕</span></p>\n')
    expect(renderProse('<div class="illust-pending">x</div>')).not.toContain('class')
    expect(renderProse('<span class="illust-pending">伪造</span>')).toContain('<span class="illust-pending">伪造</span>')
  })

  test('流式正文停在没写完的图片语法上：不报错，也不闪出原始标记', () => {
    for (const tail of ['![', '![门廊', '![门廊]', '![门廊](', '![门廊](illust:', '![门廊](illust://', '![门廊](illust://1']) {
      expect(renderProse(`他推开门。${tail}`, ILL), tail).toBe('<p>他推开门。</p>\n')
    }
    expect(renderProse('他推开门。![门廊](illust://1)', ILL)).toContain('<img')
    expect(renderProse('惊叹！门开了')).toBe('<p>惊叹！门开了</p>\n')
  })

  test('不传 illustrations 时，普通排版和以前一致', () => {
    expect(renderProse('**粗体** ![图](x.png)')).toBe('<p><strong>粗体</strong> </p>\n')
  })
})

describe('renderProse 不影响 renderMarkdown', () => {
  test('渲染过 prose 之后，renderMarkdown 仍放行外链图片、class 和链接补属性', () => {
    renderProse('<img src="https://evil.example/a.png"><span class="x">字</span>')
    const html = renderMarkdown('![图](https://example.com/a.png)\n\n<span class="note">字</span>\n\n[链接](https://example.com)')
    expect(html).toContain('<img src="https://example.com/a.png" alt="图">')
    expect(html).toContain('<span class="note">字</span>')
    expect(html).toContain('target="_blank"')
    expect(html).toContain('rel="noopener noreferrer"')
  })

  test('renderMarkdown 照旧处理 illust:// 和原始 HTML img，不走 prose 的规则', () => {
    expect(renderMarkdown('![图](illust://1)')).not.toContain('illust-pending')
    expect(renderMarkdown('<img src="https://example.com/a.png">')).toContain('<img src="https://example.com/a.png">')
    expect(renderMarkdown('标题\n===\n\n- 列表')).toMatch(/<h1>标题<\/h1>[\s\S]*<li>列表<\/li>/)
  })
})
