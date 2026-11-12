import DOMPurify from 'dompurify'
import { Marked, marked } from 'marked'

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

// NOTE: Writer 正文是 AI 生成的不可信文字，只放行小说排版用的标签；标题、列表、链接等去掉标签保留文字。
// img 只用于 Painter 的配图，span 只用于「配图生成中」占位
const PROSE_TAGS = ['p', 'br', 'em', 'strong', 'del', 'blockquote', 'hr', 'img', 'span']
const PROSE_ATTRS = ['src', 'alt', 'class', 'loading', 'decoding']

const IMAGE_SRC_RE = /^\/api\/images\/[0-9a-f]{64}$/
const ILLUST_HREF_RE = /^illust:\/\/(\d+)$/
const PENDING_CLASS = 'illust-pending'

export interface ProseIllustration {
  status: 'done' | 'failed'
  url?: string
}

// NOTE: prose 的净化钩子挂在独立的 DOMPurify 实例上，和 renderMarkdown 用的全局实例互不影响，不需要作用域标志或事后清理
const proseSanitizer = DOMPurify()
proseSanitizer.addHook('uponSanitizeElement', (node, data) => {
  // NOTE: 外链图片会在玩家浏览器里发请求（泄露 IP），所以只认后端图片接口的地址，其余整个元素删掉
  if (data.tagName === 'img' && !IMAGE_SRC_RE.test((node as Element).getAttribute('src') || '')) {
    node.parentNode?.removeChild(node)
  }
})
proseSanitizer.addHook('uponSanitizeAttribute', (node, data) => {
  // NOTE: class 只放行占位块的那一个值，防止正文里伪造能撑满屏幕的工具类
  if (data.attrName === 'class' && !(node.nodeName === 'SPAN' && data.attrValue === PENDING_CLASS)) {
    data.keepAttr = false
  }
})

function escapeAttr(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
}

// NOTE: parse 是同步的，所以用模块变量把本次的配图状态递给 renderer，省得每次渲染都新建 Marked 实例
let currentIllustrations: Record<number, ProseIllustration> = {}

// NOTE: breaks 打开：旧的纯文本正文用单个换行分段，不打开会被并成一段
const proseMarked = new Marked({
  breaks: true,
  renderer: {
    image({ href, text }) {
      const m = ILLUST_HREF_RE.exec(href)
      if (!m) return ''
      const ill = currentIllustrations[Number(m[1])]
      if (!ill) return `<span class="${PENDING_CLASS}">配图生成中…</span>`
      if (ill.status !== 'done' || !ill.url) return ''
      return `<img src="${escapeAttr(ill.url)}" alt="${escapeAttr(text)}" loading="lazy" decoding="async">`
    },
  },
})

// NOTE: 流式正文可能停在没写完的图片语法上（`![说明](illust:`），去掉这段尾巴，避免闪现一截原始标记
const PARTIAL_IMAGE_TAIL_RE = /!\[[^\]\n]*\]?(?:\([^)\n]*)?$/

export function renderProse(
  md: string | null | undefined,
  illustrations: Record<number, ProseIllustration> = {},
): string {
  currentIllustrations = illustrations
  let html: string
  try {
    html = proseMarked.parse((md || '').replace(PARTIAL_IMAGE_TAIL_RE, ''), { async: false })
  } finally {
    currentIllustrations = {}
  }
  return proseSanitizer.sanitize(html, { ALLOWED_TAGS: PROSE_TAGS, ALLOWED_ATTR: PROSE_ATTRS })
}
