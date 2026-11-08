// @vitest-environment node
import { describe, expect, test } from 'vitest'
import { initialPreview, pinchTo, toggleZoom, touchDist, zoomBy } from './imagePreview'

describe('imagePreview', () => {
  test('zoomBy 限制在 1 到 4 倍，回到 1 倍时平移归零', () => {
    const s = { scale: 2, tx: 30, ty: -10 }
    expect(zoomBy(s, 0.5)).toStrictEqual({ scale: 2.5, tx: 30, ty: -10 })
    expect(zoomBy(s, 10).scale).toBe(4)
    expect(zoomBy(s, -5)).toStrictEqual({ scale: 1, tx: 0, ty: 0 })
    expect(zoomBy(initialPreview(), 0.25).scale).toBe(1.25)
  })

  test('toggleZoom：放大状态复位，原始状态放大到 2.5 倍', () => {
    expect(toggleZoom({ scale: 2, tx: 5, ty: 5 })).toStrictEqual(initialPreview())
    expect(toggleZoom(initialPreview()).scale).toBe(2.5)
  })

  test('pinchTo 按两指距离比例缩放并限幅', () => {
    expect(pinchTo(initialPreview(), 1, 100, 200).scale).toBe(2)
    expect(pinchTo(initialPreview(), 2, 100, 500).scale).toBe(4)
    expect(pinchTo({ scale: 2, tx: 9, ty: 9 }, 2, 100, 10)).toStrictEqual(initialPreview())
  })

  test('touchDist', () => {
    expect(touchDist({ clientX: 0, clientY: 0 }, { clientX: 3, clientY: 4 })).toBe(5)
  })
})
