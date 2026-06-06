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

                    // ── Character forms ───────────────────────────────────────────────────
                    editChar: null,
                    regenningAppearance: false,
                    regenningBackstory: false,
                    regenningTraits: false,
                    charForm: { name: '', race: '人类', age: 25, gender: '', occupation: '', birthplace: '', backstory: '', traits: '' },
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
                    waitingSince: null,

                    // SSE streaming
                    streaming: false,
                    writerBuffer: '',   // accumulates SSE 'token' events (Writer agent narrative)
                    narrationBuffer: '',   // accumulates SSE 'narration' events (KP direct speech)

                    // Multi-player waiting
                    waitingForPlayers: false,
                    waitingInfo: { pending: 0, total: 0 },
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
                    scenarioUploadFile: null,
                    viewingScenario: null,
                    scenarioGenForm: { name: '', theme: '', era: '', brief: '', target_length: 'short', min_players: 1, max_players: 4, difficulty: 'normal' },
                    rechargeForm: { user_id: '', amount: 100, note: '' },
                    providerForm: { name: '', provider: 'openai', base_url: '', api_key: '', is_active: true },
                    editingProvider: null,
                    pingLoading: null,
                    newShopItem: { name: '', description: '', item_type: 'card_slot', price: 0, value: 1, is_active: true },
                    siteSettings: { require_invite_code: false },
                    inviteCodes: [],
                    inviteCodeCount: 5,

                    // ── Toast ─────────────────────────────────────────────────────────────
                    toast: { show: false, message: '', type: 'success' },

                    // ══════════════════════════════════════════════════════════════════════
                    // Init
                    // ══════════════════════════════════════════════════════════════════════
                    async init() {
                        try {
                            const ps = await this.api('GET', '/api/auth/settings/public');
                            this.requireInviteCode = !!ps.require_invite_code;
                        } catch { }
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
                            this.streaming = false; this.writerBuffer = ''; this.narrationBuffer = '';
                            this.waitingForPlayers = false; this.waitingInfo = { pending: 0, total: 0 };
                            this.waitingSince = null;
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
                        this.streaming = false; this.writerBuffer = ''; this.narrationBuffer = '';
                        this.waitingForPlayers = false; this.waitingInfo = { pending: 0, total: 0 };
                        this.waitingSince = null;
                        try {
                            const [session, msgs] = await Promise.all([
                                this.api('GET', '/api/sessions/' + id),
                                this.api('GET', '/api/sessions/' + id + '/messages'),
                            ]);
                            this.currentSession = session;
                            this.messages = this.normalizeMessages(msgs || []);
                            this.page = 'game';
                            this.startGameAutoRefresh();
                            this.$nextTick(() => this.scrollChat());
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    // ═══════════════════════════════════════════════════════
                    // Utilities
                    // ═══════════════════════════════════════════════════════
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
                        if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
                        return data;
                    },
    };
};
