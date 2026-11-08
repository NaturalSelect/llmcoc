import { useUiStore } from '@/stores/ui'
import { errorMessage } from './pagedList'

// NOTE: 旧前端里这些加载/翻页调用统一 .catch(e => showToast(e.message, 'error'))，这里封装成一个函数避免每个组件重复写
export function useToastRun() {
  const ui = useUiStore()
  return async function run(fn: () => Promise<unknown>): Promise<void> {
    try {
      await fn()
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
    }
  }
}
