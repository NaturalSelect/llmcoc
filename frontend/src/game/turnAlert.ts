// NOTE: 回合轮到提示：标题闪烁前缀与轻柔的 Web Audio API 提示音，开关持久化在 localStorage['coc_turn_sound']。

const SOUND_KEY = 'coc_turn_sound'

export function loadTurnSoundPref(): boolean {
  try {
    const raw = localStorage.getItem(SOUND_KEY)
    return raw === null ? true : raw !== '0'
  } catch {
    return true
  }
}

export function saveTurnSoundPref(enabled: boolean): void {
  try {
    localStorage.setItem(SOUND_KEY, enabled ? '1' : '0')
  } catch {
    // 隐私模式或测试环境禁用存储时静默忽略
  }
}

export function formatTurnTitle(baseTitle: string, askedMe: boolean): string {
  const clean = baseTitle.replace(/^【轮到你了】\s*/, '')
  return askedMe ? `【轮到你了】${clean}` : clean
}

// playTurnChime 播放一段极短的双音和弦提示音，在无 AudioContext 的测试环境或浏览器拦截自动播放时安全降级。
export function playTurnChime(): void {
  try {
    const Ctx =
      typeof window !== 'undefined'
        ? (window.AudioContext || (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext)
        : undefined
    if (!Ctx) return
    const ctx = new Ctx()
    const now = ctx.currentTime
    const notes = [587.33, 880] // D5 -> A5
    notes.forEach((freq, idx) => {
      const osc = ctx.createOscillator()
      const gain = ctx.createGain()
      osc.type = 'sine'
      osc.frequency.setValueAtTime(freq, now + idx * 0.11)
      gain.gain.setValueAtTime(0.0001, now + idx * 0.11)
      gain.gain.exponentialRampToValueAtTime(0.08, now + idx * 0.11 + 0.02)
      gain.gain.exponentialRampToValueAtTime(0.0001, now + idx * 0.11 + 0.24)
      osc.connect(gain)
      gain.connect(ctx.destination)
      osc.start(now + idx * 0.11)
      osc.stop(now + idx * 0.11 + 0.25)
    })
    setTimeout(() => {
      void ctx.close().catch(() => {})
    }, 500)
  } catch {
    // 无音频设备或未解锁自动播放时静默跳过
  }
}
