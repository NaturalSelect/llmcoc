<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, useId, watch } from 'vue'
import type { InventoryItem } from '@/api/characterTypes'
import { ITEM_TYPE_LABEL } from './characterLogic'

defineProps<{ item: InventoryItem }>()

const POPOVER_WIDTH = 256
const GAP = 6
const MARGIN = 8

const id = useId()
const trigger = ref<HTMLElement | null>(null)
const popover = ref<HTMLElement | null>(null)
const hovered = ref(false)
const pinned = ref(false)
const pos = reactive({ left: 0, top: 0 })
const visible = computed(() => hovered.value || pinned.value)

function close(): void {
  hovered.value = false
  pinned.value = false
}

// NOTE: 浮窗挂在 body 上用 fixed 定位，否则会被弹窗正文的 overflow 裁掉；下方放不下时翻到触发元素上方
function place(): void {
  const el = trigger.value
  if (!el) return
  const r = el.getBoundingClientRect()
  pos.left = Math.min(Math.max(MARGIN, r.left), window.innerWidth - POPOVER_WIDTH - MARGIN)
  pos.top = r.bottom + GAP
  void nextTick(() => {
    const h = popover.value?.offsetHeight ?? 0
    if (pos.top + h > window.innerHeight - MARGIN && r.top - GAP - h >= MARGIN) pos.top = r.top - GAP - h
  })
}

// NOTE: Safari 和触屏点击按钮不会让它获得焦点，所以不能只靠 blur 收起，要监听外部按下
function onPointerDown(e: PointerEvent): void {
  if (!trigger.value?.contains(e.target as Node)) close()
}

function attach(): void {
  document.addEventListener('pointerdown', onPointerDown)
  window.addEventListener('scroll', place, true)
  window.addEventListener('resize', place)
}

function detach(): void {
  document.removeEventListener('pointerdown', onPointerDown)
  window.removeEventListener('scroll', place, true)
  window.removeEventListener('resize', place)
}

watch(visible, (v) => {
  if (v) {
    place()
    attach()
  } else {
    detach()
  }
})

// NOTE: 弹窗的 Esc 监听在 document 上，浮窗打开时先拦下，只收起浮窗而不关掉整个弹窗
function onEscape(e: KeyboardEvent): void {
  if (!visible.value) return
  e.stopPropagation()
  close()
}

onBeforeUnmount(detach)
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="cursor-pointer rounded-md text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    :aria-describedby="visible ? id : undefined"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @focus="pinned = true"
    @click="pinned = true"
    @blur="pinned = false"
    @keydown.esc="onEscape"
  >
    {{ item.name }}
  </button>
  <Teleport to="body">
    <div
      v-if="visible"
      :id="id"
      ref="popover"
      role="tooltip"
      class="pointer-events-none fixed z-[55] w-64 rounded-lg border border-line-strong bg-surface-2 p-3 shadow-panel"
      :style="{ left: `${pos.left}px`, top: `${pos.top}px` }"
    >
      <p class="break-words text-sm font-semibold text-fg">{{ item.name }}</p>
      <dl class="mt-2 space-y-1 text-xs">
        <div class="flex gap-2">
          <dt class="shrink-0 text-fg-subtle">类型</dt>
          <dd class="text-fg-muted">{{ ITEM_TYPE_LABEL[item.type] }}</dd>
        </div>
        <div class="flex gap-2">
          <dt class="shrink-0 text-fg-subtle">数量</dt>
          <dd class="tabular-nums text-fg-muted">{{ item.count }}</dd>
        </div>
      </dl>
      <p class="mt-2 whitespace-pre-wrap break-words text-xs text-fg-muted">{{ item.desc || '暂无描述' }}</p>
    </div>
  </Teleport>
</template>
