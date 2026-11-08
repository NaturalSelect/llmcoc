<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import type { PreviewState } from '@/game/imagePreview'
import { initialPreview, pinchTo, toggleZoom, touchDist, zoomBy } from '@/game/imagePreview'

const props = defineProps<{ src: string }>()
const emit = defineEmits<{ close: [] }>()

const view = ref<PreviewState>(initialPreview())
let pinch: { dist: number; scale: number } | null = null
let drag: { x: number; y: number; tx: number; ty: number } | null = null

watch(
  () => props.src,
  () => {
    view.value = initialPreview()
    pinch = drag = null
  },
)

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}
document.addEventListener('keydown', onKey)

function onMove(e: MouseEvent) {
  if (!drag) return
  view.value = { ...view.value, tx: drag.tx + (e.clientX - drag.x), ty: drag.ty + (e.clientY - drag.y) }
}
function onUp() {
  drag = null
  document.removeEventListener('mousemove', onMove)
  document.removeEventListener('mouseup', onUp)
}
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKey)
  onUp()
})

function onMouseDown(e: MouseEvent) {
  if (view.value.scale <= 1) return
  e.preventDefault()
  drag = { x: e.clientX, y: e.clientY, tx: view.value.tx, ty: view.value.ty }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}

function onWheel(e: WheelEvent) {
  view.value = zoomBy(view.value, e.deltaY < 0 ? 0.25 : -0.25)
}

function onTouchStart(e: TouchEvent) {
  const t = e.touches
  if (t.length === 2) {
    pinch = { dist: touchDist(t[0], t[1]), scale: view.value.scale }
    drag = null
  } else if (t.length === 1 && view.value.scale > 1) {
    drag = { x: t[0].clientX, y: t[0].clientY, tx: view.value.tx, ty: view.value.ty }
  }
}
function onTouchMove(e: TouchEvent) {
  const t = e.touches
  if (t.length === 2 && pinch) {
    view.value = pinchTo(view.value, pinch.scale, pinch.dist, touchDist(t[0], t[1]))
  } else if (t.length === 1 && drag) {
    view.value = { ...view.value, tx: drag.tx + (t[0].clientX - drag.x), ty: drag.ty + (t[0].clientY - drag.y) }
  }
}
function onTouchEnd(e: TouchEvent) {
  if (e.touches.length < 2) pinch = null
  if (e.touches.length < 1) drag = null
}
</script>

<template>
  <Teleport to="body">
    <div v-if="src" class="fixed inset-0 z-[70] flex items-center justify-center overflow-hidden bg-ink/85" role="dialog" aria-modal="true" aria-label="图片预览" @click="emit('close')">
      <button type="button" class="absolute right-4 top-4 z-10 size-9 rounded-full bg-paper/90 text-ink" aria-label="关闭预览" @click.stop="emit('close')">✕</button>
      <div class="absolute bottom-6 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-full bg-paper/90 px-3 py-1 font-type text-sm text-ink" @click.stop>
        <button type="button" class="size-7" aria-label="缩小" @click="view = zoomBy(view, -0.5)">−</button>
        <span class="w-12 text-center">{{ Math.round(view.scale * 100) }}%</span>
        <button type="button" class="size-7" aria-label="放大" @click="view = zoomBy(view, 0.5)">＋</button>
        <button v-if="view.scale !== 1" type="button" class="px-1" aria-label="重置缩放" @click="view = initialPreview()">1:1</button>
      </div>
      <img
        :src="src"
        alt="图片预览"
        draggable="false"
        class="max-h-[92vh] max-w-[96vw] select-none object-contain"
        :class="view.scale > 1 ? 'cursor-grab' : 'cursor-zoom-in'"
        :style="{ transform: `translate(${view.tx}px, ${view.ty}px) scale(${view.scale})`, touchAction: 'none' }"
        @click.stop
        @dblclick.stop="view = toggleZoom(view)"
        @wheel.stop.prevent="onWheel"
        @touchstart.stop.passive="onTouchStart"
        @touchmove.stop.prevent="onTouchMove"
        @touchend.stop="onTouchEnd"
        @touchcancel.stop="onTouchEnd"
        @mousedown.stop="onMouseDown"
      />
    </div>
  </Teleport>
</template>
