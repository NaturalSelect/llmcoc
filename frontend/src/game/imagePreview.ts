// NOTE: 图片预览的缩放与平移计算，纯函数；缩放范围 1 到 4 倍，回到 1 倍时平移归零
export const MIN_SCALE = 1
export const MAX_SCALE = 4

export interface PreviewState {
  scale: number
  tx: number
  ty: number
}

export function initialPreview(): PreviewState {
  return { scale: 1, tx: 0, ty: 0 }
}

function withScale(state: PreviewState, scale: number): PreviewState {
  const next = Math.min(MAX_SCALE, Math.max(MIN_SCALE, scale))
  return next === MIN_SCALE ? { scale: next, tx: 0, ty: 0 } : { ...state, scale: next }
}

export function zoomBy(state: PreviewState, delta: number): PreviewState {
  return withScale(state, +(state.scale + delta).toFixed(2))
}

export function toggleZoom(state: PreviewState): PreviewState {
  return state.scale > 1 ? initialPreview() : { ...state, scale: 2.5 }
}

export function pinchTo(state: PreviewState, startScale: number, startDist: number, dist: number): PreviewState {
  return withScale(state, startScale * (dist / startDist))
}

export function touchDist(a: { clientX: number; clientY: number }, b: { clientX: number; clientY: number }): number {
  return Math.hypot(a.clientX - b.clientX, a.clientY - b.clientY)
}
