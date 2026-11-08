// NOTE: 逐行扫描所有 .vue 模板，拦住旧纸张主题的残留和不合设计规范的写法；报错信息带文件名和行号
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, test } from 'vitest'

const srcDir = fileURLToPath(new URL('..', import.meta.url))
const themePath = fileURLToPath(new URL('./theme.css', import.meta.url))

const LEGACY = 'paper-deep|paper-edge|paper|ink-soft|ink-faint|ink|seal|ledger|moss'
const PALETTE = 'white|black|gray|slate|zinc|neutral|stone|red|orange|amber|yellow|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|pink|rose'
const UTILITY = 'bg|text|border|ring|from|to|via|accent|divide|outline|decoration|fill|stroke'

const RULES: Array<{ name: string; re: RegExp }> = [
  { name: '任意像素字号（最小只能到 text-xs）', re: /text-\[\d+px\]/ },
  { name: '旧纸张主题色名', re: new RegExp(`\\b(${UTILITY})-(${LEGACY})\\b`) },
  { name: '旧字体类', re: /\bfont-(type|serif)\b/ },
  { name: '原始调色板颜色（用主题令牌）', re: new RegExp(`\\b(bg|text|border|ring)-(${PALETTE})\\b`) },
  { name: '!important 覆盖', re: /class="(?:[^"]*\s)?![a-z]/ },
  { name: 'rounded-sm', re: /\brounded-sm\b/ },
  { name: '粗边框（border-l-4 / border-2）', re: /\bborder-(l-4|2)\b/ },
  // NOTE: 不含箭头区段：正文里的「A → B」流程说明是文字，不是图标
  { name: 'emoji 或符号图标（用 @lucide/vue）', re: /[\u{1F300}-\u{1FAFF}\u{22EF}\u{2300}-\u{23FF}\u{25A0}-\u{25FF}\u{2600}-\u{27BF}\u{2B50}]/u },
]

function vueFiles(): string[] {
  return (readdirSync(srcDir, { recursive: true }) as string[]).filter((f) => f.endsWith('.vue')).map((f) => f.replaceAll('\\', '/')).sort()
}

function isComment(line: string): boolean {
  const t = line.trim()
  return t.startsWith('//') || t.startsWith('/*') || t.startsWith('*') || t.startsWith('<!--')
}

describe('设计规则', () => {
  test('扫描到了模板文件', () => {
    expect(vueFiles().length).toBeGreaterThan(50)
  })

  test.each(RULES)('没有 $name', ({ re }) => {
    const hits: string[] = []
    for (const file of vueFiles()) {
      readFileSync(srcDir + file, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (!isComment(line) && re.test(line)) hits.push(`${file}:${i + 1}  ${line.trim().slice(0, 100)}`)
        })
    }
    expect(hits).toEqual([])
  })

  test('主题里已经删掉旧色名和旧字体别名', () => {
    const css = readFileSync(themePath, 'utf8')
    const legacy = css.match(new RegExp(`--(color-(${LEGACY})|font-(type|serif)):`, 'g'))
    expect(legacy).toBeNull()
  })
})
