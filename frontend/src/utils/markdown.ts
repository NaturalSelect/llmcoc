import DOMPurify from 'dompurify'
import { marked } from 'marked'

// NOTE: 只注册一次：给清洗后的正文里的链接补上 target/rel，避免点击后离开 SPA 又留下 opener 引用
DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})

export function renderMarkdown(md: string | null | undefined): string {
  return DOMPurify.sanitize(marked.parse(md || '', { async: false }))
}

// NOTE: Writer 正文是 AI 生成的不可信文字，只放行小说排版用的标签；标题、列表、链接、图片等去掉标签保留文字
const PROSE_TAGS = ['p', 'br', 'em', 'strong', 'del', 'blockquote', 'hr']

// NOTE: breaks 打开：旧的纯文本正文用单个换行分段，不打开会被并成一段
export function renderProse(md: string | null | undefined): string {
  const html = marked.parse(md || '', { async: false, breaks: true })
  return DOMPurify.sanitize(html, { ALLOWED_TAGS: PROSE_TAGS, ALLOWED_ATTR: [] })
}
