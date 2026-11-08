import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface ToastItem {
  id: number
  message: string
  type: 'success' | 'error'
}

export interface ConfirmOptions {
  confirmText?: string
  danger?: boolean
  withInput?: boolean
  inputPlaceholder?: string
}

export interface ConfirmState {
  message: string
  confirmText: string
  danger: boolean
  withInput: boolean
  inputValue: string
  inputPlaceholder: string
  resolve: ((value: boolean | string | null) => void) | null
}

const TOAST_MS = 3500

function emptyConfirm(): ConfirmState {
  return { message: '', confirmText: '确认', danger: false, withInput: false, inputValue: '', inputPlaceholder: '', resolve: null }
}

export const useUiStore = defineStore('ui', () => {
  const toasts = ref<ToastItem[]>([])
  const confirmState = ref<ConfirmState | null>(null)
  let nextId = 1

  function toast(message: string, type: 'success' | 'error' = 'success'): void {
    const id = nextId++
    toasts.value.push({ id, message, type })
    setTimeout(() => {
      toasts.value = toasts.value.filter((t) => t.id !== id)
    }, TOAST_MS)
  }

  // NOTE: Promise 化确认框；danger 为 true 时确认按钮显示朱砂色（破坏性操作）。
  // withInput 时行为等同 prompt：确认返回输入字符串，取消返回 null；否则确认返回 true，取消返回 false。
  // 已有确认框打开时再次调用会先把上一个当作取消，避免悬挂的 Promise
  function confirm(message: string, opts: ConfirmOptions & { withInput: true }): Promise<string | null>
  function confirm(message: string, opts?: ConfirmOptions): Promise<boolean>
  function confirm(message: string, opts: ConfirmOptions = {}): Promise<boolean | string | null> {
    resolveConfirm(false)
    return new Promise((resolve) => {
      confirmState.value = {
        ...emptyConfirm(),
        message,
        confirmText: opts.confirmText || '确认',
        danger: !!opts.danger,
        withInput: !!opts.withInput,
        inputPlaceholder: opts.inputPlaceholder || '',
        resolve,
      }
    })
  }

  function resolveConfirm(ok: boolean): void {
    const st = confirmState.value
    if (!st) return
    confirmState.value = null
    st.resolve?.(ok ? (st.withInput ? st.inputValue : true) : st.withInput ? null : false)
  }

  return { toasts, confirmState, toast, confirm, resolveConfirm }
})
