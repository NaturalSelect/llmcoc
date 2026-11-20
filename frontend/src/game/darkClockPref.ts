// NOTE: 管理员是否隐藏暗钟，持久化在 localStorage['coc_hide_dark_clocks']，默认显示。

const HIDE_KEY = 'coc_hide_dark_clocks'

export function loadHideDarkClocksPref(): boolean {
  try {
    return localStorage.getItem(HIDE_KEY) === '1'
  } catch {
    return false
  }
}

export function saveHideDarkClocksPref(hidden: boolean): void {
  try {
    localStorage.setItem(HIDE_KEY, hidden ? '1' : '0')
  } catch {
    // 隐私模式或测试环境禁用存储时静默忽略
  }
}
