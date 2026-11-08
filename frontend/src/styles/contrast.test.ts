// NOTE: 读 theme.css 里的色值，按 WCAG 公式算对比度；深色界面最容易在这里出问题（灰字发糊、彩色按钮反白看不清）
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, test } from 'vitest'

const css = readFileSync(fileURLToPath(new URL('./theme.css', import.meta.url)), 'utf8')

function color(name: string): [number, number, number] {
  const m = css.match(new RegExp(`--color-${name}:\\s*#([0-9a-fA-F]{6})\\s*;`))
  if (!m) throw new Error(`theme.css 里没有 --color-${name} 的十六进制色值`)
  const n = parseInt(m[1], 16)
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
}

function luminance([r, g, b]: [number, number, number]): number {
  const lin = (c: number) => {
    const s = c / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b)
}

function ratio(fg: string, bg: string): number {
  const a = luminance(color(fg))
  const b = luminance(color(bg))
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

describe('主题对比度', () => {
  const backgrounds = ['bg', 'surface', 'surface-2']

  test.each(['fg', 'fg-muted', 'fg-subtle'].flatMap((f) => backgrounds.map((b) => [f, b])))('文字 %s 在 %s 上至少 4.5', (f, b) => {
    expect(ratio(f, b)).toBeGreaterThanOrEqual(4.5)
  })

  test('主按钮上的深色字在强调色上至少 4.5', () => {
    expect(ratio('accent-fg', 'accent')).toBeGreaterThanOrEqual(4.5)
  })

  test.each(['accent', 'danger', 'success', 'info', 'warning'])('强调色 %s 在 surface 上至少 4.5', (c) => {
    expect(ratio(c, 'surface')).toBeGreaterThanOrEqual(4.5)
  })

  test.each(['hp', 'san', 'mp'])('属性条颜色 %s 在 surface-2 上至少 3', (c) => {
    expect(ratio(c, 'surface-2')).toBeGreaterThanOrEqual(3)
  })
})
