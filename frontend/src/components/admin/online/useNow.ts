import { onBeforeUnmount, ref, watch } from 'vue'
import type { Ref } from 'vue'

// NOTE: 耗时显示用的时钟。只在 active 为真时才有定时器，且全组件只有这一个；卸载时一定清掉，避免页面离开后还在每秒触发
export function useNow(active: Ref<boolean>, intervalMs = 1000): Ref<number> {
  const now = ref(Date.now())
  let timer: ReturnType<typeof setInterval> | null = null

  function stop(): void {
    if (timer === null) return
    clearInterval(timer)
    timer = null
  }

  watch(
    active,
    (on) => {
      if (!on) return stop()
      now.value = Date.now()
      if (timer === null) timer = setInterval(() => (now.value = Date.now()), intervalMs)
    },
    { immediate: true },
  )
  onBeforeUnmount(stop)
  return now
}
