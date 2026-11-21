<script lang="ts">
import { ref } from 'vue'

// NOTE: 同一时刻只固定一个物品详情，点击另一项自动切换，避免多个固定浮窗重叠遮挡
const activePinnedKey = ref<symbol | null>(null)

export function clearPinnedItemDetail(): void {
  activePinnedKey.value = null
}
</script>

<script setup lang="ts">
import { Pin } from '@lucide/vue'
import { computed, nextTick, onBeforeUnmount, reactive, useId, watch } from 'vue'
import type { InventoryItem } from '@/api/characterTypes'
import { ITEM_TYPE_LABEL } from './characterLogic'

const props = defineProps<{ item: InventoryItem }>()

const POPOVER_WIDTH = 256
const GAP = 6
const MARGIN = 8

const id = useId()
const pinKey = Symbol()
const trigger = ref<HTMLElement | null>(null)
const popover = ref<HTMLElement | null>(null)
const hovered = ref(false)
const focused = ref(false)
const pos = reactive({ left: 0, top: 0 })
let pointerInteracting = false
let pointerTimer: ReturnType<typeof setTimeout> | null = null

const pinned = computed(() => activePinnedKey.value === pinKey)
const visible = computed(() => pinned.value || (activePinnedKey.value === null && (hovered.value || focused.value)))

function clearPointerFlag(): void {
  pointerInteracting = false
  if (pointerTimer !== null) {
    clearTimeout(pointerTimer)
    pointerTimer = null
  }
}

function close(): void {
  hovered.value = false
  focused.value = false
  if (activePinnedKey.value === pinKey) activePinnedKey.value = null
}

// NOTE: 鼠标点击会依次触发 pointerdown -> focus -> click，忽略本次 focus 避免和点击切换固定冲突
function onPointerDown(): void {
  pointerInteracting = true
  if (pointerTimer !== null) clearTimeout(pointerTimer)
  pointerTimer = setTimeout(() => {
    pointerInteracting = false
    pointerTimer = null
  }, 0)
}

function onFocus(): void {
  if (pointerInteracting) {
    clearPointerFlag()
    return
  }
  focused.value = true
}

function onBlur(): void {
  focused.value = false
}

function togglePin(): void {
  clearPointerFlag()
  if (pinned.value) {
    close()
    return
  }
  activePinnedKey.value = pinKey
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

function attach(): void {
  window.addEventListener('scroll', place, true)
  window.addEventListener('resize', place)
}

function detach(): void {
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

// NOTE: ChipList 按下标复用节点，物品被装备、卸下、丢弃或切换卡导致同位置换了物品时重置状态
watch(
  [() => props.item.name, () => props.item.desc ?? '', () => props.item.type, () => props.item.equipped ?? false],
  () => close(),
)

// NOTE: 弹窗的 Esc 监听在 document 上，浮窗打开时先拦下，只收起浮窗而不关掉整个弹窗
function onEscape(e: KeyboardEvent): void {
  if (!visible.value) return
  e.stopPropagation()
  close()
}

onBeforeUnmount(() => {
  clearPointerFlag()
  if (activePinnedKey.value === pinKey) activePinnedKey.value = null
  detach()
})
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="inline-flex cursor-pointer items-center gap-1 rounded-md text-left focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    :class="pinned ? 'font-medium text-accent' : ''"
    :aria-describedby="visible ? id : undefined"
    :aria-pressed="pinned"
    @pointerdown="onPointerDown"
    @mouseenter="hovered = true"
    @mouseleave="hovered = false"
    @focus="onFocus"
    @blur="onBlur"
    @click="togglePin"
    @keydown.esc="onEscape"
  >
    <span>{{ item.name }}</span>
    <Pin v-if="pinned" :size="12" :stroke-width="2" class="shrink-0" aria-hidden="true" />
  </button>
  <Teleport to="body">
    <div
      v-if="visible"
      :id="id"
      ref="popover"
      role="tooltip"
      class="fixed z-[55] w-64 rounded-lg border bg-surface-2 p-3 shadow-panel"
      :class="pinned ? 'pointer-events-auto border-accent/50' : 'pointer-events-none border-line-strong'"
      :style="{ left: `${pos.left}px`, top: `${pos.top}px` }"
    >
      <div class="flex items-start justify-between gap-2">
        <p class="break-words text-sm font-semibold text-fg">{{ item.name }}</p>
        <Pin v-if="pinned" :size="13" :stroke-width="2" class="mt-0.5 shrink-0 text-accent" aria-hidden="true" />
      </div>
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
