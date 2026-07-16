// LLM-COC Core App
// All reactive state + auth + navigation + utilities
window.COC = window.COC || {};

window.COC.core = function() {
    return {
                    // ── Core ──────────────────────────────────────────────────────────────
                    page: 'auth',
                    modal: null,
                    loading: false,

                    // ── Auth ──────────────────────────────────────────────────────────────
                    authTab: 'login',
                    authError: '',
                    token: localStorage.getItem('coc_token') || '',
                    user: null,
                    loginForm: { username: '', password: '' },
                    regForm: { username: '', email: '', password: '', invite_code: '' },
                    requireInviteCode: false,

                    // ── Data ──────────────────────────────────────────────────────────────
                    characters: [],
                    sessions: [],
                    myHistory: [],
                    myFavorites: [],
                    sessionsTab: 'lobby',
                    scenarioList: [],
                    shopItems: [],
                    transactions: [],
                    // NOTE: 列表首屏加载态，用于骨架屏显示
                    dashboardLoading: false,
                    sessionsLoading: false,
                    shopLoading: false,
                    // NOTE: 商城费率配置，由 loadShopCosts 从后端加载
                    shopCosts: {},

                    // ── Character forms ───────────────────────────────────────────────────
                    editChar: null,
                    regenningAppearance: false,
                    regenningBackstory: false,
                    regenningTraits: false,
                    charForm: { name: '', race: '人类', age: 25, gender: '', occupation: '', birthplace: '', residence: '', backstory: '', appearance: '', traits: '', creation_hint: '' },
                    createCharMode: 'ai',
                    manualDraft: null,
                    manualStep: 1,
                    manualSkillDefaults: {},
                    manualSkills: {},
                    manualSkillBudget: { occupation: 0, interest: 0, total: 0 },
                    manualSkillNames: [],
                    genForm: { name: '', race: '人类', gender: '', age: '', occupation: '', era: '', background: '' },

                    // ── Session forms ─────────────────────────────────────────────────────
                    sessionForm: { name: '', scenario_id: '', max_players: 4, password: '' },
                    joinForm: { character_card_id: '', password: '' },
                    joinTargetSession: null,

                    // ── Game state ────────────────────────────────────────────────────────
                    currentSession: null,
                    messages: [],
                    chatInput: '',
                    endingSession: false,
                    leavingSession: false,
                    revivingSession: false,
                    sessionRefreshTimer: null,
                    refreshingSession: false,
                    refreshingMessages: false,
                    refreshingChatStatus: false,
                    chatStatusPollTimer: null,
                    waitingSince: null,

                    // NOTE: SSE流式状态
                    streaming: false,
                    activeStreamID: null,
                    writerBuffer: '',   // 兼容旧token事件,新白字直接写入对应消息
                    narrationBuffer: '',   // 当前KP主流程回复
                    imageBuffer: [],   // NOTE: 当前SSE临时图片,刷新后不保留
                    progressText: '',   // 当前后端真实处理阶段
                    // NOTE: 连接恢复状态（SSE断线后轮询恢复期间为true）
                    connectionRecovering: false,

                    // Multi-player waiting
                    waitingForPlayers: false,
                    waitingInfo: { pending: 0, total: 0, submitted_names: [], pending_names: [] },
                    preSendMessageCount: -1, // snapshot of messages.length before local user push
                    refreshIntervals: {
                        gameAuto: 5000,
                        waitingPoll: 4000,
                        recoveryPoll: 4000,
                    },

                    // ── Shop ──────────────────────────────────────────────────────────────
                    purchasing: false,
                    shopTargetCharacterId: '',
                    inventoryInput: '',

                    // ── Revive ────────────────────────────────────────────────────────────
                    deadCharacters: [],
                    reviving: false,

                    // ── Admin ─────────────────────────────────────────────────────────────
                    adminTab: 'users',
                    adminHTML: '',
                    adminLoaded: false,
                    adminUsers: [],
                    adminProviders: [],
                    adminAgents: [],
                    adminScenarios: [],
                    adminScenarioPage: 1,
                    adminScenarioPageSize: 20,
                    adminScenarioTotal: 0,
                    adminScenarioTotalPages: 1,
                    adminShopItems: [],
                    cacheStats: null,
                    cacheKeys: [],
                    cacheKeyPage: 1,
                    cacheKeyPageSize: 20,
                    cacheKeyTotal: 0,
                    cacheKeyTotalPages: 1,
                    selectedCacheEntry: null,
                    cacheEntryLoading: false,
                    scenarioUploadFile: null,
                    viewingScenario: null,
                    scenarioGenerationLog: null,
                    scenarioGenerationLogLoading: false,
                    scenarioGenForm: { name: '', theme: '', era: '', brief: '', target_length: 'short', min_players: 1, max_players: 4, difficulty: 'normal' },
                    // NOTE: AI 模组生成的 SSE 流式状态：running 表示流式请求进行中，logs 为实时进度日志
                    scenarioGenRunning: false,
                    scenarioGenLogs: [],
                    rechargeForm: { user_id: '', amount: 100, note: '' },
                    providerForm: { name: '', provider: 'openai', base_url: '', api_key: '', is_active: true },
                    editingProvider: null,
                    agentPingLoading: null,
                    newShopItem: { name: '', description: '', item_type: 'card_slot', price: 0, value: 1, is_active: true },
                    siteSettings: {
                        require_invite_code: false,
                        initial_coins: 600,
                        initial_card_slots: 3,
                        regenerate_appearance_cost: 100,
                        regenerate_backstory_cost: 100,
                        regenerate_traits_cost: 100,
                        revive_base_cost: 2000,
                        end_session_cost: 200,
                        writer_history_max_runes: 20000,
                        balance_rules: '',
                    },
                    inviteCodes: [],
                    inviteCodeCount: 5,

                    // ── Toast ─────────────────────────────────────────────────────────────
                    toast: { show: false, message: '', type: 'success' },

                    // ── Confirm dialog（自定义确认框，替代原生 confirm/prompt） ───────────
                    confirmState: { message: '', confirmText: '确认', danger: false, withInput: false, inputValue: '', inputPlaceholder: '', resolve: null },

                    // ══════════════════════════════════════════════════════════════════════
                    // Init
                    // ══════════════════════════════════════════════════════════════════════
                    async init() {
                        try {
                            const ps = await this.api('GET', '/api/auth/settings/public');
                            this.requireInviteCode = !!ps.require_invite_code;
                        } catch { }
                        // NOTE: 加载商城费率配置，无需鉴权
                        this.loadShopCosts().catch(() => {});
                        if (this.token) {
                            try {
                                await this.loadMe();
                                await Promise.all([this.loadCharacters(), this.loadDeadCharacters(), this.loadSessions(), this.loadMyHistory(), this.loadMyFavorites()]);
                                this.goTo('dashboard');
                            } catch {
                                this.token = '';
                                localStorage.removeItem('coc_token');
                            }
                        }
                    },

                    // ══════════════════════════════════════════════════════════════════════
                    // Auth
                    // ══════════════════════════════════════════════════════════════════════
                    async login() {
                        this.loading = true; this.authError = '';
                        try {
                            const r = await this.api('POST', '/api/auth/login', this.loginForm);
                            this.setToken(r.token); this.user = r.user;
                            await Promise.all([this.loadCharacters(), this.loadDeadCharacters(), this.loadSessions(), this.loadMyHistory(), this.loadMyFavorites()]);
                            this.goTo('dashboard');
                        } catch (e) { this.authError = e.message; }
                        this.loading = false;
                    },

                    async register() {
                        this.loading = true; this.authError = '';
                        try {
                            const r = await this.api('POST', '/api/auth/register', this.regForm);
                            this.setToken(r.token); this.user = r.user;
                            await Promise.all([this.loadCharacters(), this.loadDeadCharacters(), this.loadSessions(), this.loadMyHistory(), this.loadMyFavorites()]);
                            this.goTo('dashboard');
                        } catch (e) { this.authError = e.message; }
                        this.loading = false;
                    },

                    logout() {
                        this.stopGameAutoRefresh();
                        this.stopChatStatusPolling();
                        this.token = ''; localStorage.removeItem('coc_token');
                        this.user = null; this.page = 'auth';
                    },
                    setToken(t) { this.token = t; localStorage.setItem('coc_token', t); },

                    // ══════════════════════════════════════════════════════════════════════
                    // Data loaders
                    async loadMe() { this.user = await this.api('GET', '/api/auth/me'); },
                    // ══════════════════════════════════════════════════════════════════════
                    // Navigation
                    // ══════════════════════════════════════════════════════════════════════
                    goTo(p) {
                        if (this.page === 'game') {
                            this.stopGameAutoRefresh();
                            this.stopChatStatusPolling();
                            this.streaming = false; this.activeStreamID = null; this.writerBuffer = ''; this.narrationBuffer = ''; this.imageBuffer = []; this.progressText = '';
                            this.waitingForPlayers = false; this.waitingInfo = { pending: 0, total: 0, submitted_names: [], pending_names: [] };
                            this.waitingSince = null;
                            this.connectionRecovering = false;
                        }
                        this.page = p;
                        if (p === 'sessions') {
                            this.loadSessions().catch(e => this.showToast(e.message, 'error'));
                            this.loadMyHistory().catch(e => this.showToast(e.message, 'error'));
                        }
                        if (p === 'shop') this.loadShop().catch(e => this.showToast(e.message, 'error'));
                        if (p === 'admin') {
                            this.loadAdminPage().catch(e => this.showToast(e.message, 'error'));
                            this.loadAdminUsers().catch(e => this.showToast(e.message, 'error'));
                            this.loadAdminProviders().catch(e => this.showToast(e.message, 'error'));
                            this.loadAdminAgents().catch(e => this.showToast(e.message, 'error'));
                            this.loadAdminScenarios().catch(e => this.showToast(e.message, 'error'));
                            this.loadAdminShopItems().catch(e => this.showToast(e.message, 'error'));
                            this.loadSiteSettings().catch(e => this.showToast(e.message, 'error'));
                            this.loadInviteCodes().catch(e => this.showToast(e.message, 'error'));
                        }
                    },

                    async goToSession(id) {
                        this.stopChatStatusPolling();
                        this.streaming = false; this.activeStreamID = null; this.writerBuffer = ''; this.narrationBuffer = ''; this.imageBuffer = []; this.progressText = '';
                        this.waitingForPlayers = false; this.waitingInfo = { pending: 0, total: 0, submitted_names: [], pending_names: [] };
                        this.waitingSince = null;
                        this.connectionRecovering = false;
                        try {
                            const [session, msgs, chatStatus] = await Promise.all([
                                this.api('GET', '/api/sessions/' + id),
                                this.api('GET', '/api/sessions/' + id + '/messages'),
                                this.api('GET', '/api/sessions/' + id + '/chat-status'),
                            ]);
                            this.currentSession = session;
                            this.messages = this.normalizeMessages(msgs || []);
                            this.page = 'game';
                            this.startGameAutoRefresh();
                            if (this.applyChatStatus(chatStatus)) {
                                this.pollChatStatus();
                            }
                            this.$nextTick(() => this.scrollChat(true));
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    // ═══════════════════════════════════════════════════════
                    // Utilities
                    // ═══════════════════════════════════════════════════════
                    // NOTE: 从后端加载商城各项金币费率
                    async loadShopCosts() {
                        try {
                            const costs = await this.api('GET', '/api/shop/costs');
                            this.shopCosts = costs || {};
                        } catch (_) {
                            this.shopCosts = {};
                        }
                    },
                    fmtTime(iso) {
                        if (!iso) return '';
                        return new Date(iso).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
                    },
                    fmtDate(iso) {
                        if (!iso) return '';
                        return new Date(iso).toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
                    },
                    showToast(msg, type = 'success') {
                        this.toast = { show: true, message: msg, type };
                        setTimeout(() => { this.toast.show = false; }, 3500);
                    },
                    // NOTE: Promise 化确认框；danger 为 true 时确认按钮显示红色（破坏性操作）；
                    // withInput 时行为等同 prompt：确认返回输入字符串，取消返回 null
                    confirmDialog(message, opts = {}) {
                        return new Promise((resolve) => {
                            this.confirmState = {
                                message,
                                confirmText: opts.confirmText || '确认',
                                danger: !!opts.danger,
                                withInput: !!opts.withInput,
                                inputValue: '',
                                inputPlaceholder: opts.inputPlaceholder || '',
                                resolve,
                            };
                            this.modal = 'confirm';
                        });
                    },
                    resolveConfirm(ok) {
                        const st = this.confirmState;
                        const r = st?.resolve;
                        this.modal = null;
                        this.confirmState = { message: '', confirmText: '确认', danger: false, withInput: false, inputValue: '', inputPlaceholder: '', resolve: null };
                        if (r) r(ok ? (st.withInput ? st.inputValue : true) : (st.withInput ? null : false));
                    },
                    async api(method, path, body) {
                        const opts = {
                            method,
                            headers: {
                                'Content-Type': 'application/json',
                                ...(this.token ? { 'Authorization': 'Bearer ' + this.token } : {}),
                            },
                        };
                        if (body !== undefined && method !== 'GET') opts.body = JSON.stringify(body);
                        const resp = await fetch(path, opts);
                        const data = await resp.json().catch(() => ({}));
                        if (!resp.ok) {
                            const details = Array.isArray(data.details) ? '：' + data.details.join('；') : '';
                            throw new Error((data.error || `HTTP ${resp.status}`) + details);
                        }
                        return data;
                    },
    };
};
