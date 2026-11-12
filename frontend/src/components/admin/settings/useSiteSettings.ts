import { reactive } from 'vue'
import type { SiteSettingRow, SiteSettings } from '@/api/adminTypesContent'
import { api } from '@/api/client'
import { errorMessage } from '@/components/admin/scenarios/pagedList'
import { useUiStore } from '@/stores/ui'
import {
  balanceRulesLength,
  BALANCE_RULES_MAX,
  type CostKey,
  costValueToSubmit,
  DEFAULT_SITE_SETTINGS,
  LAWYER_SIMILARITY_MAX,
  LAWYER_SIMILARITY_MIN,
  parseSiteSettings,
  similarityValueToSubmit,
} from './settingsLogic'

export type ToggleKey = 'require_invite_code' | 'allow_nsfw'

// NOTE: 站点设置的状态与全部写操作；toast 文案、校验顺序、接口路径均与旧 admin.js 保持一致
export function useSiteSettings() {
  const ui = useUiStore()
  const settings = reactive<SiteSettings>({ ...DEFAULT_SITE_SETTINGS })

  async function load(): Promise<void> {
    const rows = (await api<SiteSettingRow[]>('GET', '/api/admin/config/settings')) || []
    Object.assign(settings, parseSiteSettings(rows))
  }

  async function put(key: string, value: string): Promise<void> {
    await api('PUT', '/api/admin/config/settings/' + key, { value })
  }

  async function toggle(key: ToggleKey): Promise<void> {
    const newVal = settings[key] ? 'false' : 'true'
    try {
      await put(key, newVal)
      settings[key] = newVal === 'true'
      ui.toast('设置已更新')
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
    }
  }

  // NOTE: 数字输入框被清空时 v-model.number 会给出空字符串，这里放宽写入类型，非法值由 saveCost 的校验拦住
  function setCost(key: CostKey, v: unknown): void {
    settings[key] = v as number
  }

  async function saveCost(key: CostKey): Promise<void> {
    const val = costValueToSubmit(settings[key])
    if (val === null) {
      ui.toast('请输入有效的正整数', 'error')
      return
    }
    try {
      await put(key, val)
      ui.toast('设置已更新')
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
    }
  }

  // NOTE: 同 setCost：输入框清空时会得到空字符串，非法值由 saveSimilarity 拦住
  function setSimilarity(v: unknown): void {
    settings.lawyer_cache_min_similarity = v as number
  }

  async function saveSimilarity(): Promise<void> {
    const val = similarityValueToSubmit(settings.lawyer_cache_min_similarity)
    if (val === null) {
      ui.toast(`相似度阈值需为 ${LAWYER_SIMILARITY_MIN} 到 ${LAWYER_SIMILARITY_MAX} 之间的数字`, 'error')
      return
    }
    try {
      await put('lawyer_cache_min_similarity', val)
      ui.toast('相似度阈值已保存')
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
    }
  }

  async function saveBalanceRules(): Promise<void> {
    const val = settings.balance_rules || ''
    if (balanceRulesLength(val) > BALANCE_RULES_MAX) {
      ui.toast('平衡调整规则不能超过 2000 个字符', 'error')
      return
    }
    try {
      await put('balance_rules', val)
      ui.toast('平衡调整规则已保存')
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
    }
  }

  // FIXME: 旧前端结束后会顺带刷新大厅的房间列表；新前端的大厅页进入时自行加载，这里不跨页面刷新
  async function endAllSessions(): Promise<void> {
    const ok = await ui.confirm('确认强制结束所有运行中的房间？此操作不可逆，不会向玩家收取结束游戏费用。', {
      danger: true,
      confirmText: '一键结束',
    })
    if (!ok) return
    try {
      const r = await api<{ message?: string }>('POST', '/api/admin/sessions/end-all')
      ui.toast(r.message || '已结束所有房间')
    } catch (e) {
      ui.toast(errorMessage(e), 'error')
    }
  }

  return { settings, load, toggle, setCost, saveCost, setSimilarity, saveSimilarity, saveBalanceRules, endAllSessions }
}
