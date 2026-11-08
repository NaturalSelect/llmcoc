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
