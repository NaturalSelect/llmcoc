import { useUiStore, type ConfirmOptions } from '@/stores/ui'

// NOTE: ConfirmHost 的 Esc 会冒泡到 document，而 BaseModal 也在 document 上监听 Esc，取消确认框会把底下的弹窗一起关掉。
// 确认框存在期间，以及它被关闭后的同一次事件分发内（setTimeout 0 之后才解除），都忽略底下弹窗的 close
export function useConfirmGuard() {
  const ui = useUiStore()
  let pending = 0

  async function confirm(message: string, opts?: ConfirmOptions): Promise<boolean> {
    pending++
    try {
      return await ui.confirm(message, opts)
    } finally {
      setTimeout(() => {
        pending--
      }, 0)
    }
  }

  function guardClose(close: () => void): () => void {
    return () => {
      if (pending === 0) close()
    }
  }

  return { confirm, guardClose }
}
