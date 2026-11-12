import type { AgentConfigRow, AgentFormState } from '@/api/adminTypesCore'

// NOTE: 角色表分四处：ROLE_DEFAULTS（默认值）与 ROLE_LABELS / ROLE_DESCS / ROLE_HINTS（名称、描述、占位提示）。
// 新增角色时四处都要改（见 AGENTS.md「Agent 角色配置」）。

export interface RoleDefaults {
  max_tokens: number
  temperature: number
  context_window?: number
  model_name?: string
  thinking_level?: string
  is_active?: boolean
  // NOTE: 旧代码读取这些字段但 roleDefaults 里没有任何角色设置，保留以便将来按角色覆盖
  disable_temperature?: boolean
  image_via_chat?: boolean
  diffusion_mode?: boolean
  with_jailbreak?: boolean
  thinking_budget_tokens?: number
}

// NOTE: 键的顺序就是「库里缺行时」追加到界面末尾的顺序，不要随意重排
export const ROLE_DEFAULTS: Record<string, RoleDefaults> = {
  director: { max_tokens: 1500, temperature: 0.7, context_window: 0 },
  writer: { max_tokens: 800, temperature: 0.85, context_window: 300000 },
  // NOTE: writer_nsfw 仅在房间开启NSFW且本轮被标记为色情内容时启用，默认关闭。
  writer_nsfw: { max_tokens: 800, temperature: 0.85, context_window: 300000, is_active: false },
  lawyer: { max_tokens: 800, temperature: 0.3 },
  npc: { max_tokens: 600, temperature: 0.9 },
  // NOTE: npc_nsfw 仅在房间开启NSFW且本次act_npc调用被标记为色情内容时启用，默认关闭。
  npc_nsfw: { max_tokens: 600, temperature: 0.9, is_active: false },
  painter: { max_tokens: 0, temperature: 0, model_name: 'dall-e-3', thinking_level: 'none', is_active: false },
  // NOTE: painter_nsfw 仅在 Director 把配图标记为 nsfw=true 时启用，默认关闭；未启用时这类配图请求会被拒绝。
  painter_nsfw: { max_tokens: 0, temperature: 0, model_name: 'dall-e-3', thinking_level: 'none', is_active: false },
  // NOTE: painter_hq 只用于调查员立绘，默认关闭；未启用时立绘生成直接报错，不回落 Painter。
  painter_hq: { max_tokens: 0, temperature: 0, model_name: 'gpt-image-1', thinking_level: 'none', is_active: false },
  // NOTE: diffusion_translator 把画面描述翻译成扩散模型标签，仅在 Painter 开启扩散模型模式时被调用；低温度减少擅自添加画面元素。
  diffusion_translator: { max_tokens: 800, temperature: 0.3 },
  architect: { max_tokens: 4000, temperature: 0.7 },
  qa_guard: { max_tokens: 2000, temperature: 0.3 },
  parser: { max_tokens: 4000, temperature: 0.1 },
  // NOTE: compiler 负责把故事文本编译为结构化模组JSON，低温度减少擅自发挥。
  compiler: { max_tokens: 4000, temperature: 0.15 },
  // NOTE: dramaturg 是可选的剧构顾问，由Director按需通过工具调用咨询，默认关闭，需管理员单独绑定 provider 才会被路由。
  dramaturg: { max_tokens: 500, temperature: 0.5, is_active: false },
  // NOTE: embedding 给 Lawyer 规则缓存做语义检索，只走嵌入接口，不用 token 上限和温度；默认关闭，未启用时缓存退回标签匹配。
  embedding: { max_tokens: 0, temperature: 0, model_name: 'text-embedding-3-small', thinking_level: 'none', is_active: false },
}

// NOTE: scripter 角色已下线，后端旧库可能还残留该行，界面不显示
const HIDDEN_ROLES = new Set(['scripter'])

export const EXPECTED_ROLES: string[] = Object.keys(ROLE_DEFAULTS).filter((r) => !HIDDEN_ROLES.has(r))

const FALLBACK_DEFAULTS: RoleDefaults = { max_tokens: 1024, temperature: 0.7 }

const ROLE_LABELS: Record<string, string> = {
  director: '🎬 Director',
  writer: '✍️ Writer',
  writer_nsfw: '🔞 NSFW Writer',
  lawyer: '⚖️ Lawyer',
  npc: '🎭 NPC',
  npc_nsfw: '🔞 NSFW NPC',
  painter: '🎨 Painter',
  painter_nsfw: '🔞 NSFW Painter',
  painter_hq: '🖼️ 持久 Painter',
  diffusion_translator: '🏷️ Diffusion Translator',
  architect: '🏗️ Architect',
  qa_guard: '🔍 QA Guard',
  parser: '🔧 Parser',
  // NOTE: compiler — 故事转模组编译
  compiler: '🛠️ Compiler',
  dramaturg: '🎭 Dramaturg',
  embedding: '🧬 Embedding',
}

const ROLE_DESCS: Record<string, string> = {
  director: '场景导演 — 决策工具调用序列',
  writer: '叙事撰写 — 生成面向玩家的克苏鲁叙述（流式输出）；会看到 Director 的完整上下文，需选支持工具调用的模型',
  writer_nsfw: '成人向叙事 — 仅在房间开启NSFW且本轮被标记为色情内容时启用；未配置或关闭时自动回落默认 Writer（默认关闭）；同样需选支持工具调用的模型',
  lawyer: '规则顾问 — 查阅规则书提供裁决依据',
  npc: 'NPC扮演 — 给出场景NPC的行动与对话',
  npc_nsfw: '成人向NPC互动 — 仅在房间开启NSFW且本次act_npc调用被标记为色情内容时启用；未配置或关闭时自动回落默认 NPC（默认关闭）',
  painter: '画图代理 — 按需生成场景图片（默认关闭，不落库）',
  painter_nsfw: '成人向画图 — Director 把配图标记为 nsfw=true 时只路由到它；未启用时这类配图请求会被直接拒绝，不回落默认 Painter（默认关闭）',
  painter_hq: '持久画图 — 生成调查员立绘等长期保存的图片；未启用时立绘生成直接报错，不回落默认 Painter（默认关闭）',
  diffusion_translator: '扩散提示词翻译 — Painter 开启扩散模型模式时，把导演的自然语言画面描述翻译成标签式提示词；未启用时扩散模式的配图会失败',
  architect: '模组设计 — 生成大纲并转化为完整JSON模组',
  qa_guard: '质量审查 — 审查模组可玩性、规则合规与线索设计',
  parser: 'JSON修复 — 低温度结构化输出，修复其他Agent的JSON格式错误',
  // NOTE: compiler — 只做故事文本到结构化JSON的忠实转换，无权改写剧情事实
  compiler: '模组编译 — 将故事文本忠实编译为结构化模组JSON；建议配置低温度模型',
  // NOTE: dramaturg — 剧构顾问，Director按需咨询，输入经脱敏处理不含玩家原始数据，只给建议不操作游戏状态（默认关闭）
  dramaturg: '剧构顾问 — Director按需咨询，输入已脱敏不含玩家数据，给出节奏与走向建议（默认关闭）',
  embedding: '语义检索 — Lawyer 规则缓存的向量检索；只支持 OpenAI 兼容的 /embeddings 接口（Anthropic 类型不支持）；未启用时退回标签匹配；换模型后已有缓存向量会在后台重算（默认关闭）',
}

const ROLE_HINTS: Record<string, string> = {
  director: '留空使用内置 Director 提示词（JSON工具调用格式）',
  writer: '留空使用内置 Writer 提示词（克苏鲁散文风格）',
  writer_nsfw: '为成人向场景单独绑定更宽松的 provider/模型；未启用时自动使用默认 Writer（默认关闭）',
  lawyer: '留空使用内置 Lawyer 提示词（规则查阅模式）',
  npc: '留空使用内置 NPC 提示词',
  npc_nsfw: '为成人向NPC互动单独绑定更宽松的 provider/模型；未启用时自动使用默认 NPC（默认关闭）',
  painter: '配置 OpenAI 兼容图片模型，如 dall-e-3（默认关闭）',
  painter_nsfw: '为成人向配图单独绑定更宽松的图片模型；未启用时 nsfw=true 的配图会被拒绝（默认关闭）',
  painter_hq: '为立绘单独绑定高质量图片模型，如 gpt-image-1；开启扩散模式时需要启用 diffusion_translator（默认关闭）',
  diffusion_translator: '留空使用内置提示词（输出一行 Danbooru 风格标签）；建议配置低温度、指令遵循强的文本模型',
  architect: '留空使用内置 Architect 提示词（大纲+JSON生成）',
  qa_guard: '留空使用内置 QA Guard 提示词（模组质量审查）',
  parser: '留空使用内置 Parser 提示词（JSON格式修复）',
  // NOTE: compiler 留空使用内置 Compiler 提示词（故事转模组编译）
  compiler: '留空使用内置 Compiler 提示词（故事转模组编译）；建议配置低温度模型',
  // NOTE: dramaturg 未绑定 provider 时工具不可见，Director回退自身节奏判断规则（默认关闭）
  dramaturg: '留空使用内置 Dramaturg 提示词（节奏顾问）；不配置则Director自行判断节奏（默认关闭）',
  embedding: '嵌入模型不使用提示词，此处留空；需绑定 OpenAI 兼容 Provider（不支持 Anthropic 类型），如 text-embedding-3-small；未启用时 Lawyer 缓存退回标签匹配（默认关闭）',
}

// NOTE: 用 hasOwn 而不是直接下标，避免 role 恰好是 constructor 之类的原型属性名时取到函数
function pick(table: Record<string, string>, role: string): string | undefined {
  return Object.hasOwn(table, role) ? table[role] : undefined
}

export function agentLabel(role: string): string {
  return pick(ROLE_LABELS, role) || role
}

export function agentDesc(role: string): string {
  return pick(ROLE_DESCS, role) || ''
}

export function agentDefaultHint(role: string): string {
  return pick(ROLE_HINTS, role) || '留空使用默认'
}

function rowToForm(r: AgentConfigRow): AgentFormState {
  return {
    id: r.id,
    role: r.role,
    // NOTE: 后端未绑定时返回 null，下拉框用空串表示「使用全局配置」
    provider_config_id: r.provider_config_id ?? '',
    model_name: r.model_name,
    max_tokens: r.max_tokens,
    context_window: r.context_window,
    temperature: r.temperature,
    disable_temperature: r.disable_temperature,
    image_via_chat: r.image_via_chat,
    diffusion_mode: r.diffusion_mode,
    with_jailbreak: r.with_jailbreak,
    thinking_level: r.thinking_level,
    thinking_budget_tokens: r.thinking_budget_tokens,
    system_prompt: r.system_prompt,
    is_active: r.is_active,
  }
}

function defaultsToForm(role: string): AgentFormState {
  const d = ROLE_DEFAULTS[role] ?? FALLBACK_DEFAULTS
  return {
    id: 'new-' + role,
    role,
    provider_config_id: '',
    model_name: d.model_name || '',
    max_tokens: d.max_tokens,
    context_window: d.context_window || 0,
    temperature: d.temperature,
    disable_temperature: d.disable_temperature || false,
    image_via_chat: d.image_via_chat || false,
    diffusion_mode: d.diffusion_mode || false,
    with_jailbreak: d.with_jailbreak || false,
    thinking_level: d.thinking_level || '',
    thinking_budget_tokens: d.thinking_budget_tokens || 0,
    system_prompt: '',
    is_active: d.is_active !== undefined ? d.is_active : true,
  }
}

// NOTE: 与旧 loadAdminAgents 一致：先放库里已有的行（按后端顺序，同角色后者覆盖前者，隐藏角色除外），
// 再把库里缺行的预期角色按 ROLE_DEFAULTS 顺序补在末尾，保证每个可配置角色在界面上都存在
export function buildAgentForms(rows: AgentConfigRow[] | null | undefined): AgentFormState[] {
  const byRole = new Map<string, AgentConfigRow>()
  for (const r of rows || []) {
    if (!HIDDEN_ROLES.has(r.role)) byRole.set(r.role, r)
  }
  const forms = [...byRole.values()].map(rowToForm)
  for (const role of EXPECTED_ROLES) {
    if (!byRole.has(role)) forms.push(defaultsToForm(role))
  }
  return forms
}
