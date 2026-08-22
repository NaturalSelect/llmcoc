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
                    allowNSFW: true,

                    // ── Data ──────────────────────────────────────────────────────────────
                    characters: [],
                    sessions: [],
                    sessionPage: 1,
                    sessionPageSize: 20,
                    sessionTotal: 0,
                    sessionTotalPages: 1,
                    myHistory: [],
                    myHistoryPage: 1,
                    myHistoryPageSize: 20,
                    myHistoryTotal: 0,
                    myHistoryTotalPages: 1,
                    myFavorites: [],
                    myFavoritesPage: 1,
                    myFavoritesPageSize: 20,
                    myFavoritesTotal: 0,
                    myFavoritesTotalPages: 1,
                    sessionsTab: 'lobby',
                    scenarioList: [],
                    scenarioPage: 1,
                    scenarioPageSize: 20,
                    scenarioTotal: 0,
                    scenarioTotalPages: 1,
                    selectedScenario: null,
                    shopItems: [],
                    shopItemPage: 1,
                    shopItemPageSize: 20,
                    shopItemTotal: 0,
                    shopItemTotalPages: 1,
                    transactions: [],
                    transactionPage: 1,
                    transactionPageSize: 20,
                    transactionTotal: 0,
                    transactionTotalPages: 1,
                    // NOTE: 列表首屏加载态，用于骨架屏显示
                    dashboardLoading: false,
                    sessionsLoading: false,
                    shopLoading: false,
                    // NOTE: 商城费率配置，由 loadShopCosts 从后端加载
                    shopCosts: {},

                    // ── Character forms ───────────────────────────────────────────────────
                    editChar: null,
                    appearanceGuidance: '',
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
                    // 游戏页角色状态条：HP/SAN 下降时的一次性高亮标记
                    hudFlash: { hp: false, san: false },
                    _hudLastStats: null,
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

                        // ── Warehouse (账号级仓库) ────────────────────────────────────────────
                        warehouse: [],
                        warehouseLoading: false,
                        warehouseTargetCharId: null,


                    // ── Revive ────────────────────────────────────────────────────────────
                    deadCharacters: [],
                    reviving: false,

                    // ── Admin ─────────────────────────────────────────────────────────────
                    adminTab: 'users',
                    adminHTML: '',
                    adminLoaded: false,
                    adminUsers: [],
                    adminUserPage: 1,
                    adminUserPageSize: 20,
                    adminUserTotal: 0,
                    adminUserTotalPages: 1,
                    // 管理员为指定玩家角色卡添加物品的弹窗状态
                    adminInventoryTarget: null,       // { user_id, username } — 当前操作的目标用户
                    adminInventoryCards: [],          // 目标用户的角色卡列表
                    adminInventorySelectedCard: null, // 选中的角色卡对象（含 inventory）
                    adminInventoryInput: '',          // 物品输入框内容
                    adminInventoryLoading: false,     // 加载/提交中状态
                    adminProviders: [],
                    adminAgents: [],
                    adminScenarios: [],
                    adminScenarioPage: 1,
                    adminScenarioPageSize: 20,
                    adminScenarioTotal: 0,
                    adminScenarioTotalPages: 1,
                    adminShopItems: [],
                    adminShopItemPage: 1,
                    adminShopItemPageSize: 20,
                    adminShopItemTotal: 0,
                    adminShopItemTotalPages: 1,
                    cacheStats: null,
                    llmStats: null,
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
                    generationLogTab: 'story',
                    scenarioGenForm: { name: '', theme: '', era: '', brief: '', target_length: 'short', min_players: 1, max_players: 4, difficulty: 'normal', count: 1 },
                    // NOTE: AI 模组生成的 SSE 流式状态：running 表示流式请求进行中，logs 为实时进度日志，
                    // batchStatus 记录批量生成的整体进度与每个子任务结果（current/total/succeeded/failed/results）
                    scenarioGenRunning: false,
                    scenarioGenLogs: [],
                    scenarioGenBatchStatus: null,
                    // NOTE: 上传故事编译：管理员只上传故事文档文件，跳过 AI 故事生成阶段，
                    // 神话锚点与奖励概念由后端 anchor_extract 阶段自动从文档内容识别。
                    compileStoryForm: { name: '' },
                    compileStoryRunning: false,
                    compileStoryLogs: [],
                    rechargeForm: { user_id: '', amount: 100, note: '' },
                    providerForm: { name: '', provider: 'openai', base_url: '', api_key: '', is_active: true },
                    editingProvider: null,
                    agentPingLoading: null,
                    newShopItem: { name: '', description: '', item_type: 'card_slot', price: 0, value: 1, is_active: true },
                    siteSettings: {
                        require_invite_code: false,
                        allow_nsfw: true,
                        allow_nsfw_images: true,
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
                    inviteCodePage: 1,
                    inviteCodePageSize: 20,
                    inviteCodeTotal: 0,
                    inviteCodeTotalPages: 1,
                    inviteCodeCount: 5,

                    // ── Toast ─────────────────────────────────────────────────────────────
                    toast: { show: false, message: '', type: 'success' },

                    // ── Confirm dialog（自定义确认框，替代原生 confirm/prompt） ───────────
                    confirmState: { message: '', confirmText: '确认', danger: false, withInput: false, inputValue: '', inputPlaceholder: '', resolve: null },

                    // ── Image preview（点击图片放大预览，支持鼠标滚轮/触摸双指缩放与拖动平移） ─
                    preview: { src: '', scale: 1, tx: 0, ty: 0 },
                    _previewDrag: null,
                    _previewPinch: null,

                    // ══════════════════════════════════════════════════════════════════════
                    // Init
                    // ══════════════════════════════════════════════════════════════════════
                    async init() {
                        try {
                            const ps = await this.api('GET', '/api/auth/settings/public');
                            this.requireInviteCode = !!ps.require_invite_code;
                            this.allowNSFW = ps.allow_nsfw !== false;
                        } catch { }
                        // NOTE: 加载商城费率配置，无需鉴权
                        this.loadShopCosts().catch(() => {});
                        if (this.token) {
                            try {
                                await this.loadMe();
                                await Promise.all([this.loadCharacters(), this.loadDeadCharacters(), this.loadSessions()]);
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
                            await Promise.all([this.loadCharacters(), this.loadDeadCharacters(), this.loadSessions()]);
                            this.goTo('dashboard');
                        } catch (e) { this.authError = e.message; }
                        this.loading = false;
                    },

                    async register() {
                        this.loading = true; this.authError = '';
                        try {
                            const r = await this.api('POST', '/api/auth/register', this.regForm);
                            this.setToken(r.token); this.user = r.user;
                            await Promise.all([this.loadCharacters(), this.loadDeadCharacters(), this.loadSessions()]);
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
                    fmtDuration(ms) {
                        if (!ms) return '';
                        if (ms < 1000) return `${ms}ms`;
                        return `${(ms / 1000).toFixed(1)}s`;
                    },
                    fmtStepAvg(ms, steps) {
                        if (!ms || !steps) return '';
                        return this.fmtDuration(Math.round(ms / steps));
                    },
                    difficultyLabel(v) {
                        const map = { easy: '简单', normal: '普通', hard: '困难' };
                        return map[(v || 'normal').toLowerCase()] || v;
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
                    // ── Image preview ────────────────────────────────────────────────────
                    openImagePreview(src) {
                        if (!src) return;
                        this.preview = { src, scale: 1, tx: 0, ty: 0 };
                        this.modal = 'imagePreview';
                    },
                    closeImagePreview() {
                        if (this.modal !== 'imagePreview') return;
                        this.modal = null;
                        this.preview = { src: '', scale: 1, tx: 0, ty: 0 };
                        this._previewDrag = null;
                        this._previewPinch = null;
                    },
                    previewZoomBy(delta) {
                        const next = Math.min(4, Math.max(1, +(this.preview.scale + delta).toFixed(2)));
                        this.preview.scale = next;
                        if (next === 1) { this.preview.tx = 0; this.preview.ty = 0; }
                    },
                    previewReset() {
                        this.preview.scale = 1;
                        this.preview.tx = 0;
                        this.preview.ty = 0;
                    },
                    previewToggleZoom() {
                        if (this.preview.scale > 1) this.previewReset();
                        else this.preview.scale = 2.5;
                    },
                    previewWheel(e) {
                        this.previewZoomBy(e.deltaY < 0 ? 0.25 : -0.25);
                    },
                    _touchDist(touches) {
                        const dx = touches[0].clientX - touches[1].clientX;
                        const dy = touches[0].clientY - touches[1].clientY;
                        return Math.hypot(dx, dy);
                    },
                    onPreviewTouchStart(e) {
                        const t = e.touches;
                        if (t.length === 2) {
                            this._previewPinch = { dist: this._touchDist(t), scale: this.preview.scale };
                            this._previewDrag = null;
                        } else if (t.length === 1 && this.preview.scale > 1) {
                            this._previewDrag = { startX: t[0].clientX, startY: t[0].clientY, startTx: this.preview.tx, startTy: this.preview.ty };
                        }
                    },
                    onPreviewTouchMove(e) {
                        const t = e.touches;
                        if (t.length === 2 && this._previewPinch) {
                            const ratio = this._touchDist(t) / this._previewPinch.dist;
                            const next = Math.min(4, Math.max(1, this._previewPinch.scale * ratio));
                            this.preview.scale = next;
                            if (next === 1) { this.preview.tx = 0; this.preview.ty = 0; }
                        } else if (t.length === 1 && this._previewDrag) {
                            this.preview.tx = this._previewDrag.startTx + (t[0].clientX - this._previewDrag.startX);
                            this.preview.ty = this._previewDrag.startTy + (t[0].clientY - this._previewDrag.startY);
                        }
                    },
                    onPreviewTouchEnd(e) {
                        if (e.touches.length < 2) this._previewPinch = null;
                        if (e.touches.length < 1) this._previewDrag = null;
                    },
                    onPreviewMouseDown(e) {
                        if (this.preview.scale <= 1) return;
                        e.preventDefault();
                        const start = { x: e.clientX, y: e.clientY, tx: this.preview.tx, ty: this.preview.ty };
                        const onMove = (ev) => {
                            this.preview.tx = start.tx + (ev.clientX - start.x);
                            this.preview.ty = start.ty + (ev.clientY - start.y);
                        };
                        const onUp = () => {
                            document.removeEventListener('mousemove', onMove);
                            document.removeEventListener('mouseup', onUp);
                        };
                        document.addEventListener('mousemove', onMove);
                        document.addEventListener('mouseup', onUp);
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
                    // NOTE: 渐进式拉取某个分页接口的全部数据。
                    // 用于下拉选卡等必须拿到完整数据集的场景（如人物卡列表被加入房间/商城复活/管理员加物品等多处直接复用），
                    // 不能只拉一页；每页之间加短间隔避免连续请求压垮后端。onProgress 可用于边拉边刷新界面。
                    async fetchAllPages(path, { pageSize = 100, delayMs = 120, onProgress } = {}) {
                        const sep = path.includes('?') ? '&' : '?';
                        let page = 1;
                        let all = [];
                        while (true) {
                            const resp = await this.api('GET', `${path}${sep}page=${page}&page_size=${pageSize}`);
                            all = all.concat(resp?.items || []);
                            if (onProgress) onProgress(all.slice());
                            const totalPages = Math.max(1, Number(resp?.total_pages || 1));
                            if (page >= totalPages) break;
                            page += 1;
                            await new Promise(r => setTimeout(r, delayMs));
                        }
                        return all;
                    },
    };
};
