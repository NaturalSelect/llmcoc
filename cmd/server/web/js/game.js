// LLM-COC Game — Chat, SSE streaming, auto-refresh
window.COC = window.COC || {};
window.COC.game = {
                    // ═══════════════════════════════════════════════════════
                    // Chat / SSE
                    // ═══════════════════════════════════════════════════════
                    async sendChat() {
                        const content = String(this.chatInput).trim();
                        if (!content || !this.canSpeak()) return false;

                        const submittedAt = new Date();
                        const optimisticMessage = {
                            id: Date.now(), role: 'user',
                            username: this.currentPlayerDisplayName(), content,
                            created_at: submittedAt.toISOString(),
                            // NOTE: 乐观消息标记：临时 id 与服务端自增 id 永不相同，
                            // 刷新时按 user_id+内容匹配替换，未确认前保留在列表末尾。
                            _optimistic: true,
                            _new: true,
                        };
                        const formData = new FormData();
                        formData.append('content', content);

                        return this._runKPStream('/api/sessions/' + this.currentSession.id + '/chat', {
                            kind: 'chat', body: formData, content, optimisticMessage, submittedAt,
                        });
                    },

                    // NOTE: 让 KP 在没有玩家输入时主动发言（开场 / 房主跳过未回应的玩家）。
                    // 与 sendChat 共用同一套 SSE 处理，一次请求可能包含多个 KP 段。
                    async requestKPTurn() {
                        if (!this.currentSession?.id || this.page !== 'game') return false;
                        if (this.streaming || this.connectionRecovering) return false;
                        return this._runKPStream('/api/sessions/' + this.currentSession.id + '/kp-turn', {
                            kind: 'kp-turn', submittedAt: new Date(),
                        });
                    },

                    // 房主点击「让 KP 继续」：有被点名但未回应的玩家时先确认。
                    async hostContinueKP() {
                        if (!this.canKPContinue || this.streaming || this.connectionRecovering) return false;
                        const sp = this.spotlight;
                        if (sp && Array.isArray(sp.user_ids) && sp.user_ids.length > 0) {
                            if (!await this.confirmDialog('跳过未回应的玩家并让 KP 继续？', { confirmText: '让 KP 继续' })) return false;
                        }
                        return this.requestKPTurn();
                    },

                    // NOTE: /chat 与 /kp-turn 的共享流程：占用输入、发请求、解析 SSE、收尾。
                    // 每个请求的流状态都放在局部对象 st 里（而不是组件字段），这样一次请求里的
                    // 多个 KP 段互不串扰，也不会和之后新开的请求共用 assistantMessageID。
                    async _runKPStream(url, opts = {}) {
                        const isChat = opts.kind !== 'kp-turn';
                        const content = opts.content || '';
                        const optimisticMessage = opts.optimisticMessage || null;
                        const submittedAt = opts.submittedAt || new Date();

                        this.stopChatStatusPolling();

                        const streamID = Date.now() + ':' + Math.random().toString(16).slice(2);
                        const unlockKP = () => {
                            if (this.activeStreamID === streamID) {
                                this.streaming = false;
                                this.activeStreamID = null;
                            }
                        };
                        const removeOptimistic = () => {
                            if (!optimisticMessage) return;
                            const idx = this.messages.indexOf(optimisticMessage);
                            if (idx >= 0) this.messages.splice(idx, 1);
                        };

                        if (isChat) this.chatInput = '';
                        this.streaming = true;
                        this.activeStreamID = streamID;
                        this.canKPContinue = false;
                        this.writerBuffer = '';
                        this.narrationBuffer = '';
                        this.imageBuffer = [];
                        this.progressText = isChat ? '已收到行动,正在整理本轮信息' : 'KP 正在推进剧情…';

                        const tailAnchor = this.messages.length;
                        this.preSendMessageCount = tailAnchor;
                        if (optimisticMessage) {
                            this.messages.push(optimisticMessage);
                        }
                        this.$nextTick(() => this.scrollChat(true));

                        const st = {
                            anchor: tailAnchor,
                            submittedAt,
                            unlockKP,
                            // 本地 spotlight 过期时 kp_done 会提前解锁；spotlight 事件揭示 KP 仍在续跑时用它重新锁上。
                            // 只在仍停留在原房间游戏页、且没有别的流占用输入时才重锁，避免离开房间后误锁定其他房间。
                            sessionID: this.currentSession?.id,
                            relockKP: () => {
                                if (this.activeStreamID || this.page !== 'game' || this.currentSession?.id !== st.sessionID) return;
                                this.streaming = true;
                                this.activeStreamID = streamID;
                            },
                            // KP 驱动房间：kp_done 时不能立刻解锁，后面可能还有自动续跑的段。
                            kpDriven: !isChat || !!this.spotlight,
                            seenSpotlight: false,
                            kpDoneCount: 0,
                            receivedWaiting: false,
                            receivedDone: false,
                            receivedError: false,
                            // 当前段（最近一个 kp_done）的状态
                            committedKP: false,
                            assistantMessageID: null,
                            writerText: '',
                        };

                        const abortCtrl = new AbortController();
                        const abortTimer = setTimeout(() => abortCtrl.abort(), 10 * 60 * 1000);

                        try {
                            const fetchOpts = {
                                method: 'POST',
                                headers: { 'Authorization': 'Bearer ' + this.token },
                                signal: abortCtrl.signal,
                            };
                            if (opts.body) fetchOpts.body = opts.body;
                            const resp = await fetch(url, fetchOpts);

                            if (!resp.ok) {
                                const err = await resp.json().catch(() => ({ error: '请求失败' }));
                                removeOptimistic();
                                if (isChat) this.chatInput = content;
                                this.showToast(err.error || (isChat ? '发送失败' : 'KP 继续失败'), 'error');
                                unlockKP();
                                if (resp.status === 409) {
                                    const status = await this.refreshChatStatus(true);
                                    if (status && (status.processing || (status.waiting_for_players && status.submitted))) {
                                        this.pollChatStatus();
                                    }
                                } else if (!isChat) {
                                    // 403/400/404：同步一次状态，让「让 KP 继续」按钮跟上服务端。
                                    await this.refreshChatStatus(true);
                                }
                                return false;
                            }

                            await this._consumeKPStream(resp, st);
                        } catch (e) {
                            if (e.name !== 'AbortError') {
                                this.showToast(e.message || '网络错误', 'error');
                            }
                            this.progressText = '';
                        } finally {
                            clearTimeout(abortTimer);
                            unlockKP();
                        }

                        // 没跑出任何 KP 段就报错（没被点名被拒、或主流程失败没有落库）：撤回乐观消息、还原输入并同步最新点名状态。
                        // NOTE: 老房间保持原行为（不撤回乐观消息），只有 KP 驱动房间才做这一步。
                        // 本地 spotlight 可能还没被轮询刷新过（刚开局的几秒内），所以先向服务端核实房间类型。
                        if (st.receivedError && st.kpDoneCount === 0 && !st.receivedWaiting) {
                            let kpDrivenRoom = st.kpDriven;
                            if (!kpDrivenRoom && isChat && this.currentSession?.id) {
                                try {
                                    const probe = await this.api('GET', '/api/sessions/' + this.currentSession.id + '/chat-status');
                                    kpDrivenRoom = !!(probe && probe.spotlight);
                                } catch (_) { }
                            }
                            if (kpDrivenRoom) {
                                removeOptimistic();
                                if (isChat && !String(this.chatInput).trim()) this.chatInput = content;
                                this.progressText = '';
                                await this.refreshChatStatus(true);
                                return true;
                            }
                        }

                        if (st.receivedWaiting) {
                            this.writerBuffer = '';
                            this.narrationBuffer = '';
                            this.imageBuffer = [];
                            this.progressText = '';
                            this.waitingForPlayers = true;
                            this.waitingSince = submittedAt;
                            this.pollChatStatus();
                        } else if (st.receivedDone && !st.committedKP) {
                            if (this.writerBuffer || this.narrationBuffer || this.imageBuffer.length) {
                                this._commitStreamBuffers({ anchor: tailAnchor });
                            }
                        } else if (!st.receivedDone && !st.receivedError) {
                            if (this.writerBuffer || this.narrationBuffer || this.imageBuffer.length) {
                                this._commitStreamBuffers({ anchor: tailAnchor });
                            }
                            this._recoverFromDrop(submittedAt, opts.kind);
                            return true;
                        }

                        // KP 驱动房间：流结束后以服务端状态为准校正点名与「让 KP 继续」按钮。
                        if (!st.receivedWaiting && (st.kpDriven || this.spotlight)) {
                            if (st.receivedError) await this.refreshCurrentMessages(true);
                            await this.refreshChatStatus(true);
                        }
                        return true;
                    },

                    async _consumeKPStream(resp, st) {
                        const reader = resp.body.getReader();
                        const decoder = new TextDecoder();
                        let buf = '';
                        let currentEvent = '';

                        while (true) {
                            const { done, value } = await reader.read();
                            if (done) break;

                            buf += decoder.decode(value, { stream: true });
                            const lines = buf.split('\n');
                            buf = lines.pop();

                            for (const rawLine of lines) {
                                const line = rawLine.replace(/\r$/, '');

                                if (line.startsWith('event:')) {
                                    currentEvent = line.slice(6).trim();
                                } else if (line.startsWith('data:')) {
                                    const data = line.slice(5).replace(/^ /, '');
                                    this._handleKPEvent(currentEvent, data, st);
                                } else if (line === '') {
                                    currentEvent = '';
                                }
                            }
                        }
                    },

                    _handleKPEvent(event, data, st) {
                        switch (event) {

                            case 'thinking':
                                if (!this.progressText) {
                                    this.progressText = 'Agent 团队运行中，请稍候…';
                                }
                                break;

                            case 'progress':
                                this.progressText = data || this.progressText;
                                break;

                            case 'token':
                                if (data) {
                                    if (st.assistantMessageID) {
                                        st.writerText += data;
                                        this._appendWriterToMessage(st.assistantMessageID, st.writerText);
                                    } else {
                                        this.writerBuffer += data;
                                    }
                                    this.$nextTick(() => this.scrollChat());
                                }
                                break;

                            case 'narration':
                                if (data) {
                                    this.narrationBuffer += data;
                                    this.$nextTick(() => this.scrollChat());
                                }
                                break;

                            case 'image':
                                if (this.isChatImageSource(data)) {
                                    if (st.assistantMessageID) {
                                        this._appendImageToMessage(st.assistantMessageID, data);
                                    } else {
                                        this.imageBuffer.push(data);
                                    }
                                    this.$nextTick(() => this.scrollChat());
                                }
                                break;

                            case 'waiting':
                                st.receivedWaiting = true;
                                this.waitingForPlayers = true;
                                this.waitingSince = st.submittedAt;
                                try {
                                    const info = JSON.parse(data);
                                    if (info.pending !== undefined) {
                                        // NOTE: 合并新字段（submitted_names/pending_names/batched/batch_user_ids/
                                        // encounter_label/encounter_order）到 waitingInfo
                                        this.waitingInfo = {
                                            pending: info.pending ?? 0,
                                            total: info.total ?? 0,
                                            submitted_names: Array.isArray(info.submitted_names) ? info.submitted_names : [],
                                            pending_names: Array.isArray(info.pending_names) ? info.pending_names : [],
                                            batched: !!info.batched,
                                            batch_user_ids: Array.isArray(info.batch_user_ids) ? info.batch_user_ids : [],
                                            encounter_label: info.encounter_label || '',
                                            encounter_order: Array.isArray(info.encounter_order) ? info.encounter_order : [],
                                        };
                                    }
                                } catch (_) { }
                                break;

                            case 'error':
                                st.receivedError = true;
                                this.showToast(data || '处理失败', 'error');
                                break;

                            case 'kp_done':
                                this._onKPSegmentDone(data, st);
                                break;

                            case 'spotlight':
                                this._onSpotlightEvent(data, st);
                                break;

                            case 'done':
                                st.receivedDone = true;
                                this.progressText = '';
                                if (st.assistantMessageID) {
                                    this._setMessageWriterDone(st.assistantMessageID);
                                }
                                if (!st.committedKP && !st.receivedWaiting && (this.writerBuffer || this.narrationBuffer || this.imageBuffer.length)) {
                                    const msg = this._commitStreamBuffers({ anchor: st.anchor });
                                    if (msg) st.assistantMessageID = msg.id;
                                    st.committedKP = !!msg;
                                }
                                break;
                        }
                    },

                    // 每个 kp_done 开启一条新的助手消息：把当前缓冲区落成消息，并重置本段的消息 id / Writer 文本。
                    _onKPSegmentDone(data, st) {
                        st.kpDoneCount++;
                        // 服务端等上一段 Writer 收尾后才会跑下一段，所以上一段此刻必然已写完。
                        if (st.assistantMessageID) {
                            this._setMessageWriterDone(st.assistantMessageID);
                        }
                        st.assistantMessageID = null;
                        st.writerText = '';
                        st.committedKP = false;

                        let info = {};
                        try { info = JSON.parse(data || '{}') || {}; } catch (_) { info = {}; }
                        st.assistantMessageID = info.message_id || null;
                        const msg = this._commitStreamBuffers({
                            id: st.assistantMessageID || undefined,
                            created_at: info.created_at,
                            writerStreaming: !!info.has_writer && !info.writer_done,
                            anchor: st.anchor,
                            // 服务端已建好这条消息：即使本段旁白剥掉标签后为空（正文由 Writer 稍后补写）也要先占位，后续 token 才有地方写。
                            // 只对 KP 驱动房间启用，老房间行为不变。
                            force: st.kpDriven && !!st.assistantMessageID,
                        });
                        if (msg) st.assistantMessageID = msg.id;
                        st.committedKP = !!msg;

                        // 老房间：KP 主流程已落库，服务端已释放处理锁，立即解锁输入（Writer 继续补写）。
                        // KP 驱动房间改由 spotlight 事件决定，避免在自动续跑期间提前解锁。
                        if (!st.kpDriven && !st.seenSpotlight) {
                            st.unlockKP();
                        }
                    },

                    _onSpotlightEvent(data, st) {
                        let info = null;
                        try { info = JSON.parse(data || '{}'); } catch (_) { info = null; }
                        if (!info) return;
                        // 用户已离开这个房间时，这条流里的点名不能写到别的房间状态里。
                        if (this.page !== 'game' || this.currentSession?.id !== st.sessionID) return;
                        st.seenSpotlight = true;
                        st.kpDriven = true;
                        this._setSpotlightFromEvent(info);
                        this.canKPContinue = false;
                        // 有人被点名：服务端已释放处理锁，被点名的玩家可以回应；
                        // 否则 KP 仍持有发言权（可能马上自动续跑），保持锁定直到整条流结束。
                        if (this.spotlight && !this.spotlight.kp_floor) {
                            st.unlockKP();
                        } else if (st.kpDoneCount > 0) {
                            st.relockKP();
                        }
                    },

                    async _recoverFromDrop(since, kind = 'chat') {
                        if (!this.currentSession?.id || this.page !== 'game') {
                            this.connectionRecovering = false;
                            return;
                        }
                        this.connectionRecovering = true;
                        await new Promise(r => setTimeout(r, this.refreshIntervals.recoveryPoll));
                        // NOTE: 等待后再次检查，用户可能已离开游戏页
                        if (!this.currentSession?.id || this.page !== 'game') {
                            this.connectionRecovering = false;
                            return;
                        }
                        try {
                            const [status, rawMessages] = await Promise.all([
                                this.api('GET', '/api/sessions/' + this.currentSession.id + '/chat-status'),
                                this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages'),
                            ]);
                            const msgs = this.normalizeMessages(rawMessages || []);
                            const kp = msgs.find(m => m.role === 'assistant' && new Date(m.created_at) > since);
                            this.connectionRecovering = false;
                            if (this.applyChatStatus(status)) {
                                await this.refreshCurrentMessages(true);
                                this.pollChatStatus();
                                return;
                            }
                            if (kp) {
                                await this.refreshCurrentSession(true);
                                await this.refreshCurrentMessages(true);
                                return;
                            }
                            this.showToast(kind === 'kp-turn' ? 'KP 上一次推进未完成，可点击「让 KP 继续」重试' : '上一条消息未完成，请重新发送', 'error');
                            return;
                        } catch (_) { }

                        if (this.currentSession?.id && this.page === 'game') {
                            this._recoverFromDrop(since, kind);
                        } else {
                            this.connectionRecovering = false;
                        }
                    },

                    applyChatStatus(status) {
                        if (!status || this.activeStreamID) return false;

                        this._applySpotlightStatus(status);

                        const waiting = status.waiting || {};
                        this.waitingInfo = {
                            pending: waiting.pending ?? 0,
                            total: waiting.total ?? 0,
                            submitted_names: Array.isArray(waiting.submitted_names) ? waiting.submitted_names : [],
                            pending_names: Array.isArray(waiting.pending_names) ? waiting.pending_names : [],
                            batched: !!waiting.batched,
                            batch_user_ids: Array.isArray(waiting.batch_user_ids) ? waiting.batch_user_ids : [],
                            encounter_label: waiting.encounter_label || '',
                            encounter_order: Array.isArray(waiting.encounter_order) ? waiting.encounter_order : [],
                        };
                        this.connectionRecovering = false;

                        if (status.processing) {
                            this.streaming = true;
                            this.waitingForPlayers = false;
                            this.waitingSince = status.started_at ? new Date(status.started_at) : this.waitingSince;
                            this.progressText = '服务器仍在处理本轮行动，请稍候…';
                            return true;
                        }

                        if (status.waiting_for_players && status.submitted) {
                            this.streaming = false;
                            this.waitingForPlayers = true;
                            this.waitingSince = status.submitted_at ? new Date(status.submitted_at) : (this.waitingSince || new Date());
                            this.progressText = '';
                            return true;
                        }

                        this.streaming = false;
                        this.waitingForPlayers = false;
                        this.waitingSince = null;
                        this.progressText = '';
                        this.resetWaitingInfo();
                        this.stopChatStatusPolling();
                        return false;
                    },

                    // NOTE: 把 chat-status 里的点名状态与「让 KP 继续」权限写进组件状态。
                    // 老房间 spotlight 为 null：两者一并清空，输入门禁退回旧逻辑。
                    _applySpotlightStatus(status) {
                        const sp = status && status.spotlight;
                        if (!sp) {
                            this.spotlight = null;
                            this.canKPContinue = false;
                            return;
                        }
                        this.spotlight = {
                            user_ids: Array.isArray(sp.user_ids) ? sp.user_ids : [],
                            names: Array.isArray(sp.names) ? sp.names : [],
                            question: sp.question || '',
                            kp_floor: !!sp.kp_floor,
                            asked_me: !!sp.asked_me,
                        };
                        this.canKPContinue = !!status.can_kp_continue;
                    },

                    // NOTE: SSE spotlight 事件不带 asked_me，按当前登录用户 id 在 user_ids 里自行判断。
                    _setSpotlightFromEvent(info) {
                        const ids = Array.isArray(info.user_ids) ? info.user_ids : [];
                        const uid = this.user?.id;
                        this.spotlight = {
                            user_ids: ids,
                            names: Array.isArray(info.names) ? info.names : [],
                            question: info.question || '',
                            kp_floor: info.kp_floor !== undefined ? !!info.kp_floor : ids.length === 0,
                            asked_me: !!uid && ids.some(id => Number(id) === Number(uid)),
                        };
                    },

                    // KP 驱动房间且游戏进行中（老房间 spotlight 为 null，结束的房间不显示点名 UI）。
                    spotlightActive() {
                        return !!this.spotlight && this.currentSession?.status === 'playing';
                    },

                    // KP 驱动房间里当前用户没被点名。
                    spotlightBlocked() {
                        return this.spotlightActive() && !this.spotlight.asked_me;
                    },

                    spotlightNamesText() {
                        return (this.spotlight?.names || []).join('、');
                    },

                    // 横幅文案：轮到自己 / 等待别人 / KP 推进中。
                    spotlightBannerText() {
                        const sp = this.spotlight;
                        if (!sp) return '';
                        const q = sp.question || '';
                        if (sp.kp_floor) return 'KP 正在推进剧情…';
                        if (sp.asked_me) return q ? '轮到你了：' + q : '轮到你了，请写下你的行动';
                        const names = this.spotlightNamesText();
                        return 'KP 正在等待 ' + (names || '玩家') + (q ? '：' + q : '');
                    },

                    // NOTE: 输入门禁的唯一入口（textarea / 发送按钮 / sendChat 共用）。
                    // 方法而不是 getter：init.js 用 Object.assign 合并各模块，getter 会在合并时被求值成静态值。
                    canSpeak() {
                        if (this.currentSession?.status !== 'playing') return false;
                        if (this.streaming || this.waitingForPlayers || this.connectionRecovering) return false;
                        if (this.spotlight) return !!this.spotlight.asked_me;
                        return true;
                    },

                    chatPlaceholder() {
                        const normal = '以角色身份写下你的行动或台词，如：我推开门，「有人在吗？」 (Ctrl+Enter 发送)';
                        if (!this.spotlightActive()) return normal;
                        const sp = this.spotlight;
                        if (sp.asked_me) {
                            return sp.question ? 'KP 问你：' + sp.question + '（Ctrl+Enter 发送）' : normal;
                        }
                        if (sp.kp_floor) return 'KP 正在推进剧情…';
                        const names = this.spotlightNamesText();
                        if (names) return 'KP 正在等待 ' + names + ' 回应…';
                        return '等待 KP 点名…';
                    },

                    async refreshChatStatus(silent = true) {
                        if (!this.currentSession?.id || this.page !== 'game' || this.refreshingChatStatus || this.activeStreamID) return null;
                        this.refreshingChatStatus = true;
                        try {
                            const status = await this.api('GET', '/api/sessions/' + this.currentSession.id + '/chat-status');
                            const active = this.applyChatStatus(status);
                            if (active) this.pollChatStatus();
                            return status;
                        } catch (e) {
                            if (!silent) this.showToast(e.message, 'error');
                            return null;
                        } finally {
                            this.refreshingChatStatus = false;
                        }
                    },

                    pollChatStatus() {
                        if (this.chatStatusPollTimer || !this.currentSession?.id || this.page !== 'game') return;
                        const sessionID = this.currentSession.id;
                        this.chatStatusPollTimer = setTimeout(async () => {
                            this.chatStatusPollTimer = null;
                            if (!this.currentSession?.id || this.currentSession.id !== sessionID || this.page !== 'game' || this.activeStreamID) return;
                            const status = await this.refreshChatStatus(true);
                            if (!this.currentSession?.id || this.currentSession.id !== sessionID || this.page !== 'game') return;
                            await this.refreshCurrentMessages(true);
                            if (status && (status.processing || (status.waiting_for_players && status.submitted))) {
                                this.pollChatStatus();
                                return;
                            }
                            if (!status && (this.streaming || this.waitingForPlayers || this.connectionRecovering)) {
                                this.pollChatStatus();
                                return;
                            }
                            await this.refreshCurrentSession(true);
                        }, this.refreshIntervals.waitingPoll);
                    },

                    stopChatStatusPolling() {
                        if (!this.chatStatusPollTimer) return;
                        clearTimeout(this.chatStatusPollTimer);
                        this.chatStatusPollTimer = null;
                    },

                    _assistantContent(writerText, narrationText) {
                        const wt = this.stripAckContent(writerText).trim();
                        const nt = this.stripAckContent(narrationText).trim();
                        if (!nt) return wt;
                        return (wt ? wt + '\n\n' : '') + 'KP:' + nt;
                    },

                    _commitStreamBuffers(meta = {}) {
                        const responseOptions = this.extractResponseOptionsPayload(this.narrationBuffer) || this.extractResponseOptionsPayload(this.writerBuffer);
                        const diceResults = this.extractDiceResults(this.narrationBuffer);
                        const timePoint = this.extractTimePoint(this.narrationBuffer);
                        const wt = this.stripAckContent(this.writerBuffer).trim();
                        const nt = this.stripAckContent(this.narrationBuffer).trim();
                        const images = Array.isArray(this.imageBuffer) ? [...this.imageBuffer] : [];
                        // NOTE: force 用于服务端已建好消息、但本段旁白剥掉标签后为空的情况（正文由 Writer 稍后补写），
                        // 否则后续 token 找不到目标消息会被丢弃。
                        if (!wt && !nt && images.length === 0 && !meta.force) return null;
                        // NOTE: 同一个服务端 id 可能已被后台尾部同步/轮询先插进列表；此时只补上流式标记，不再重复追加（x-for 的 key 是 msg.id）。
                        const dupIdx = meta.id ? this.messages.findIndex(m => String(m.id) === String(meta.id)) : -1;
                        if (dupIdx >= 0) {
                            const existing = this.messages[dupIdx];
                            const kept = {
                                ...existing,
                                response_options: existing.response_options || responseOptions,
                                _stream_committed: true,
                            };
                            this.messages.splice(dupIdx, 1, kept);
                            this.writerBuffer = '';
                            this.narrationBuffer = '';
                            this.imageBuffer = [];
                            this.$nextTick(() => this.scrollChat());
                            return kept;
                        }
                        const msg = {
                            id: meta.id || Date.now() + 1,
                            role: 'assistant', username: 'KP',
                            content: this._assistantContent(wt, nt),
                            writer_text: wt,
                            narration_text: nt,
                            images,
                            dice_results: diceResults,
                            time_point: timePoint,
                            response_options: responseOptions,
                            created_at: meta.created_at || new Date().toISOString(),
                            _writer_streaming: !!meta.writerStreaming,
                            // NOTE: 带服务端 id 的流式落地消息；_syncTailFromDB 据此保留还没被数据库快照覆盖到的后续段。
                            _stream_committed: !!meta.id,
                            _new: true,
                        };
                        this.messages.push(msg);
                        this.writerBuffer = '';
                        this.narrationBuffer = '';
                        this.imageBuffer = [];
                        this.refreshCurrentSession(true).catch(() => { });
                        this.$nextTick(() => this.scrollChat());
                        // 同步数据库尾部消息,保证多人回合里的玩家行动顺序以服务端为准。
                        this._syncTailFromDB(meta.anchor);
                        return msg;
                    },

                    _appendWriterToMessage(messageID, writerText) {
                        const idx = this.messages.findIndex(m => String(m.id) === String(messageID));
                        if (idx < 0) return;
                        const msg = this.messages[idx];
                        const wt = this.stripAckContent(writerText).trim();
                        this.messages.splice(idx, 1, {
                            ...msg,
                            writer_text: wt,
                            content: this._assistantContent(wt, msg.narration_text || ''),
                            _writer_streaming: true,
                        });
                    },

                    _setMessageWriterDone(messageID) {
                        const idx = this.messages.findIndex(m => String(m.id) === String(messageID));
                        if (idx < 0) return;
                        this.messages.splice(idx, 1, {
                            ...this.messages[idx],
                            _writer_streaming: false,
                        });
                    },

                    _appendImageToMessage(messageID, imageSrc) {
                        const idx = this.messages.findIndex(m => String(m.id) === String(messageID));
                        if (idx < 0) {
                            if (!this.imageBuffer.includes(imageSrc)) {
                                this.imageBuffer.push(imageSrc);
                            }
                            return;
                        }
                        const msg = this.messages[idx];
                        const images = Array.isArray(msg.images) ? [...msg.images] : [];
                        if (!images.includes(imageSrc)) {
                            images.push(imageSrc);
                        }
                        this.messages.splice(idx, 1, { ...msg, images });
                    },

                    async _syncTailFromDB(anchor = null, retry = 0) {
                        if (!this.currentSession?.id) return;
                        if (anchor === null || anchor === undefined) {
                            if (this.preSendMessageCount < 0) return;
                            anchor = this.preSendMessageCount;
                        }
                        if (this.preSendMessageCount === anchor) {
                            this.preSendMessageCount = -1;
                        }
                        // 复用刷新锁,避免常规轮询在这里做尾部替换时同时追加消息。
                        // NOTE: 锁被占用时延迟重试而非直接放弃——此前直接 return 会导致
                        // 尾部顺序修正丢失（玩家消息残留在 KP 回复之后）。
                        if (this.refreshingMessages) {
                            if (retry < 5) {
                                setTimeout(() => this._syncTailFromDB(anchor, retry + 1), 300);
                            }
                            return;
                        }
                        this.refreshingMessages = true;
                        try {
                            const msgs = this.normalizeMessages(
                                (await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []
                            );
                            if (msgs.length > anchor) {
                                const localByID = new Map(this.messages.map(m => [String(m.id), m]));
                                const tail = msgs.slice(anchor).map(m => {
                                    const local = localByID.get(String(m.id));
                                    const merged = { ...this._mergeStreamingMessage(local, m), _new: true };
                                    // 保留流式落地标记：较早发起的同步晚于后一段的同步返回时，后一段仍能被识别并保住。
                                    if (local && local._stream_committed) merged._stream_committed = true;
                                    return merged;
                                });
                                // NOTE: 一次请求可能连跑多段 KP：这次拉取发起时后一段可能还没落库，
                                // 其本地消息不在 tail 里，直接整段替换会把它抹掉（后续 token 也就没处可写）。
                                const tailIDs = new Set(tail.map(m => String(m.id)));
                                const localOnly = this.messages.slice(anchor)
                                    .filter(m => m && m._stream_committed && !tailIDs.has(String(m.id)));
                                // 用数据库尾部替换本地尾部,修正多人行动顺序。
                                this.messages.splice(
                                    anchor,
                                    this.messages.length - anchor,
                                    ...tail,
                                    ...localOnly
                                );
                                this.$nextTick(() => this.scrollChat());
                            }
                        } catch (_) { }
                        finally {
                            this.refreshingMessages = false;
                        }
                    },

                    // NOTE: force=true 时无条件滚到底（发送消息、进入房间等场景）；
                    // 否则仅在已贴近底部时跟随滚动，避免打断用户翻阅历史。
                    scrollChat(force = false) {
                        const el = document.getElementById('chatBox');
                        if (!el) return;
                        const threshold = 120;
                        const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
                        if (force || atBottom) el.scrollTop = el.scrollHeight;
                    },

                    async refreshCurrentSession(silent = true) {
                        if (!this.currentSession?.id || this.refreshingSession) return;
                        this.refreshingSession = true;
                        try {
                            const prevStatus = this.currentSession?.status;
                            this.currentSession = await this.api('GET', '/api/sessions/' + this.currentSession.id);
                            this._syncHudFlash(this.myCurrentCard());
                            // NOTE: 房主刚开始游戏时，其他玩家只靠 5s 轮询发现状态变化；立刻拉一次 chat-status，
                            // 让点名状态（spotlight）和 processing 尽快生效，避免输入框在开局后短暂可用。
                            if (prevStatus && prevStatus !== this.currentSession?.status && this.currentSession?.status === 'playing'
                                && this.currentSession?.created_by !== this.user?.id) {
                                this.refreshChatStatus(true).catch(() => { });
                            }
                        } catch (e) {
                            if (!silent) this.showToast(e.message, 'error');
                        } finally {
                            this.refreshingSession = false;
                        }
                    },

                    // NOTE: 消息刷新采用整体 reconcile：以服务端返回顺序为准重建列表，
                    // 合并本地流式状态；乐观消息（临时 id）按 user_id+内容匹配服务端消息，
                    // 已确认的移除、未确认的保留在末尾。修复此前"未知 id 一律追加到末尾"
                    // 导致玩家消息被排到 KP 回复之后的问题。
                    async refreshCurrentMessages(silent = true) {
                        if (!this.currentSession?.id || this.refreshingMessages) return;
                        this.refreshingMessages = true;
                        try {
                            const msgs = this.normalizeMessages(
                                (await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []
                            );

                            const localByID = new Map(this.messages.map(m => [String(m.id), m]));
                            const newMsgs = [];
                            let changed = false;

                            const merged = msgs.map(msg => {
                                const local = localByID.get(String(msg.id));
                                if (!local) {
                                    changed = true;
                                    const fresh = { ...msg, _new: true };
                                    newMsgs.push(fresh);
                                    return fresh;
                                }
                                const m = this._mergeStreamingMessage(local, msg);
                                if (
                                    local.content !== m.content ||
                                    local.writer_text !== m.writer_text ||
                                    local.narration_text !== m.narration_text ||
                                    !this.sameImages(local.images, m.images) ||
                                    local._writer_streaming !== m._writer_streaming
                                ) {
                                    changed = true;
                                }
                                return { ...local, ...m, response_options: m.response_options };
                            });

                            // 顺序兜底：本地非乐观消息的顺序必须与服务端一致。
                            const localRealIDs = this.messages.filter(m => !m._optimistic).map(m => String(m.id));
                            if (!changed && (localRealIDs.length !== merged.length ||
                                localRealIDs.some((id, i) => id !== String(merged[i].id)))) {
                                changed = true;
                            }

                            // 乐观消息：服务端已落库（同用户、同内容）的确认移除，未确认的保留在末尾。
                            const myUserID = this.user?.id;
                            const pending = [];
                            let optimisticChanged = false;
                            for (const m of this.messages) {
                                if (!m || !m._optimistic) continue;
                                const confirmed = merged.some(dm =>
                                    dm.role === 'user' &&
                                    dm.user_id && myUserID && Number(dm.user_id) === Number(myUserID) &&
                                    String(dm.content).trim() === String(m.content).trim()
                                );
                                if (confirmed) {
                                    optimisticChanged = true;
                                } else {
                                    pending.push(m);
                                }
                            }

                            if (changed || optimisticChanged) {
                                this.messages = [...merged, ...pending];
                            }

                            if (newMsgs.length > 0 && this.waitingForPlayers && this.waitingSince) {
                                const gotKP = newMsgs.some(m => m.role === 'assistant' && new Date(m.created_at) > this.waitingSince);
                                if (gotKP) {
                                    this.waitingForPlayers = false;
                                    this.waitingSince = null;
                                    this.waitingInfo = { pending: 0, total: 0, submitted_names: [], pending_names: [], batched: false, batch_user_ids: [], encounter_label: '', encounter_order: [] };
                                }
                            }
                            if (changed || optimisticChanged) {
                                this.$nextTick(() => this.scrollChat());
                            }
                        } catch (e) {
                            if (!silent) this.showToast(e.message, 'error');
                        } finally {
                            this.refreshingMessages = false;
                        }
                    },

                    startGameAutoRefresh() {
                        this.stopGameAutoRefresh();
                        this.sessionRefreshTimer = setInterval(() => {
                            if (!this.currentSession?.id || this.page !== 'game') return;
                            this.refreshCurrentSession(true).catch(() => { });
                            this.refreshChatStatus(true).catch(() => { });
                            if (!this.streaming && !this.waitingForPlayers) {
                                this.refreshCurrentMessages(true).catch(() => { });
                            }
                        }, this.refreshIntervals.gameAuto);
                    },

                    stopGameAutoRefresh() {
                        if (!this.sessionRefreshTimer) return;
                        clearInterval(this.sessionRefreshTimer);
                        this.sessionRefreshTimer = null;
                    },

                    currentPlayerDisplayName() {
                        const uid = this.user?.id;
                        const player = this.currentSession?.players?.find(p => p.user_id === uid);
                        return player?.character_card?.name || this.user?.username || '玩家';
                    },

                    myCurrentCard() {
                        const uid = this.user?.id;
                        if (!uid) return null;
                        return this.currentSession?.players?.find(p => p.user_id === uid)?.character_card || null;
                    },

                    // 乐观消息还没有服务端 user_id，先按本地标记认作自己的发言。
                    isMyMessage(msg) {
                        if (!msg || msg.role !== 'user') return false;
                        if (msg._optimistic) return true;
                        const uid = this.user?.id;
                        return !!(uid && msg.user_id && Number(msg.user_id) === Number(uid));
                    },

                    statPercent(value, max) {
                        const total = Number(max) || 0;
                        if (total <= 0) return 0;
                        return Math.max(0, Math.min(100, Math.round((Number(value) || 0) / total * 100)));
                    },

                    currentGameTime() {
                        for (let i = this.messages.length - 1; i >= 0; i--) {
                            const tp = this.messages[i]?.time_point;
                            if (tp) return tp;
                        }
                        return null;
                    },

                    // NOTE: HP/SAN 下降时闪一次高亮，比较基准随房间切换重置，避免换房误闪。
                    _syncHudFlash(card) {
                        const sid = this.currentSession?.id || null;
                        if (!card || !sid) {
                            this._hudLastStats = null;
                            return;
                        }
                        const snapshot = {
                            sid,
                            hp: Number(card.stats?.hp ?? 0),
                            san: Number(card.stats?.san ?? 0),
                        };
                        const prev = this._hudLastStats;
                        this._hudLastStats = snapshot;
                        if (!prev || prev.sid !== sid) return;
                        const flash = { hp: snapshot.hp < prev.hp, san: snapshot.san < prev.san };
                        if (!flash.hp && !flash.san) return;
                        this.hudFlash = flash;
                        setTimeout(() => { this.hudFlash = { hp: false, san: false }; }, 700);
                    },

                    normalizeMessages(messages) {
                        return messages.map(msg => this.normalizeMessage(msg));
                    },

                    _mergeStreamingMessage(local, fresh) {
                        if (!local || local.role !== 'assistant' || fresh.role !== 'assistant') {
                            return fresh;
                        }
                        const localImages = Array.isArray(local.images) ? local.images : [];
                        const freshImages = Array.isArray(fresh.images) ? fresh.images : [];
                        const mergedImages = [...freshImages];
                        const freshHasStoredURL = freshImages.some(image => typeof image === 'string' && image.startsWith('/api/images/'));
                        if (!freshHasStoredURL) {
                            for (const image of localImages) {
                                if (!mergedImages.includes(image)) {
                                    mergedImages.push(image);
                                }
                            }
                        }
                        const freshWithImages = mergedImages.length > 0
                            ? { ...fresh, images: mergedImages }
                            : fresh;
                        const localWriter = this.stripAckContent(local.writer_text || '').trim();
                        const freshWriter = this.stripAckContent(freshWithImages.writer_text || '').trim();
                        if (!local._writer_streaming || !localWriter || localWriter.length <= freshWriter.length) {
                            return freshWithImages;
                        }
                        const narrationText = freshWithImages.narration_text || local.narration_text || '';
                        return {
                            ...freshWithImages,
                            writer_text: localWriter,
                            narration_text: narrationText,
                            content: this._assistantContent(localWriter, narrationText),
                            response_options: fresh.response_options || local.response_options,
                            _writer_streaming: true,
                        };
                    },

                    sameImages(a, b) {
                        const left = Array.isArray(a) ? a : [];
                        const right = Array.isArray(b) ? b : [];
                        if (left.length !== right.length) return false;
                        return left.every((image, idx) => image === right[idx]);
                    },

                    normalizeMessage(message) {
                        if (!message || message.role !== 'assistant') {
                            return message;
                        }

                        const content = message.content || '';
                        const writerPending = this.extractWriterPending(content)
                            || this.extractWriterPending(message.narration_text || '')
                            || this.extractWriterPending(message.writer_text || '')
                            || !!message._writer_streaming;
                        const responseOptions = this.extractResponseOptionsPayload(content)
                            || this.extractResponseOptionsPayload(message.narration_text || '')
                            || this.extractResponseOptionsPayload(message.writer_text || '');
                        const images = [];
                        if (Array.isArray(message.images)) {
                            for (const image of message.images) {
                                if (this.isChatImageSource(image) && !images.includes(image)) {
                                    images.push(image);
                                }
                            }
                        }
                        if (images.length === 0 && String(content).includes('<image_data_url')) {
                            images.push(...this.extractImageDataURLs(content));
                        }
                        // 必须在 stripAckContent 之前解析：接口只返回 content，标签清理后信息就没了。
                        const narrationRaw = message.narration_text || '';
                        let diceResults = this.extractDiceResults(narrationRaw);
                        if (diceResults.length === 0) {
                            diceResults = this.extractDiceResults(content);
                        }
                        const timePoint = this.extractTimePoint(narrationRaw) || this.extractTimePoint(content);
                        message = {
                            ...message,
                            content: this.stripAckContent(content),
                            writer_text: this.stripAckContent(message.writer_text || ''),
                            narration_text: this.stripAckContent(message.narration_text || ''),
                            images,
                            dice_results: diceResults,
                            time_point: timePoint,
                            response_options: message.response_options || responseOptions,
                            _writer_streaming: !!writerPending,
                        };

                        if (message.writer_text || message.narration_text) {
                            return message;
                        }

                        const split = this.splitAssistantContent(message.content || '');
                        if (!split) {
                            return message;
                        }

                        return {
                            ...message,
                            writer_text: split.writerText,
                            narration_text: split.narrationText,
                        };
                    },

                    extractWriterPending(content) {
                        content = String(content || '');
                        if (!content.includes('<writer_pending')) return false;
                        return /<writer_pending\b[^>]*>\s*true\s*<\/writer_pending>/i.test(content);
                    },

                    extractResponseOptionsPayload(content) {
                        content = String(content || '');
                        if (!content.includes('<response_options')) return null;
                        const match = content.match(/<response_options\b[^>]*>([\s\S]*?)<\/response_options>/i);
                        if (!match) return null;
                        try {
                            const payload = JSON.parse(match[1]);
                            const options = Array.isArray(payload.options)
                                ? payload.options.map(opt => String(opt || '').trim()).filter(Boolean)
                                : [];
                            return {
                                question: String(payload.question || '').trim(),
                                options,
                            };
                        } catch (_) {
                            return null;
                        }
                    },

                    extractImageDataURLs(content) {
                        const images = [];
                        const re = /<image_data_url\b[^>]*>([\s\S]*?)<\/image_data_url>/gi;
                        let match;
                        while ((match = re.exec(String(content || ''))) !== null) {
                            const dataURL = String(match[1] || '').trim();
                            if (this.isChatImageSource(dataURL) && !images.includes(dataURL)) {
                                images.push(dataURL);
                            }
                        }
                        return images;
                    },

                    isChatImageSource(src) {
                        if (typeof src !== 'string') return false;
                        return src.startsWith('data:image/') || src.startsWith('/api/images/');
                    },

                    // NOTE: 骰子/时间由后端以内部标签追加在 KP 回复末尾（orchestrator 的
                    // formatSingleDiceResult / formatGameTime），此处解析成结构化数据单独渲染，
                    // 标签本身由 stripAckContent 清掉，不再泄漏到玩家可见正文。
                    extractDiceResults(content) {
                        content = String(content || '');
                        if (!content.includes('<dice')) return [];
                        const match = content.match(/<dice\b[^>]*>([\s\S]*?)<\/dice>/i);
                        if (!match) return [];
                        return String(match[1] || '')
                            .split(';')
                            .map(part => part.trim())
                            .filter(Boolean)
                            .map(part => {
                                // 后端格式固定为 "%v鉴定: %v 难度:%s (暗骰)"，难度与暗骰均可缺省。
                                const parsed = part.match(/^(.*?)鉴定[:：]\s*(\S+)(?:\s*难度[:：]\s*([^\s(（]+))?/);
                                // 解析不出来时降级显示原文，避免格式变化直接吞掉信息。
                                if (!parsed) return { raw: part };
                                return {
                                    what: (parsed[1] || '').trim(),
                                    roll: (parsed[2] || '').trim(),
                                    level: (parsed[3] || '').trim(),
                                };
                            });
                    },

                    extractTimePoint(content) {
                        content = String(content || '');
                        if (!content.includes('<time_point')) return null;
                        const match = content.match(/<time_point\b[^>]*>([\s\S]*?)<\/time_point>/i);
                        if (!match) return null;
                        const raw = String(match[1] || '').trim();
                        if (!raw) return null;
                        const parsed = raw.match(/^第(\d+)天\s*(\d{1,2}:\d{2})(?:[(（](.*?)[)）])?/);
                        if (!parsed) return { raw, label: raw, elapsed: '' };
                        return {
                            raw,
                            label: '第' + parsed[1] + '天 ' + parsed[2],
                            elapsed: (parsed[3] || '').trim(),
                        };
                    },

                    stripAckContent(content) {
                        content = String(content || '');
                        if (!content.includes('<')) {
                            return content.trim();
                        }
                        return content
                            .replace(/<image_ref\b[^>]*(?:\/>|>\s*<\/image_ref>)/gi, '')
                            .replace(/<(ack|direction|response_options|writer_pending|image_data_url|dice|time_point)\b[^>]*>[\s\S]*?<\/\1>/gi, '')
                            .replace(/<(ack|direction|response_options|writer_pending|image_data_url|dice|time_point)\b[^>]*>[\s\S]*$/gi, '')
                            // NOTE: SSE 按固定字符数切片推送，流式中途缓冲区尾部必然出现 "<dic" 这类半截标签。
                            .replace(/<[a-z_]*$/i, '')
                            .trim();
                    },

                    activeResponseSuggestions() {
                        // NOTE: KP 驱动房间里只有被点名的人才能回应，且只看最新一条 KP 消息的选项；
                        // 老房间保持原逻辑（往前找最近一条带选项的 KP 消息）。
                        const kpDriven = !!this.spotlight;
                        if (kpDriven && !this.spotlight.asked_me) return null;
                        for (let i = this.messages.length - 1; i >= 0; i--) {
                            const msg = this.messages[i];
                            if (msg.role === 'user') return null;
                            if (msg.role !== 'assistant') continue;
                            if (
                                msg.response_options &&
                                Array.isArray(msg.response_options.options) &&
                                msg.response_options.options.length > 0
                            ) {
                                return msg.response_options;
                            }
                            if (kpDriven) return null;
                        }
                        return null;
                    },

                    appendResponseSuggestion(option) {
                        const text = String(option || '').trim();
                        if (!text) return;
                        // KP 驱动房间里没被点名的玩家不能往输入框塞内容（输入框已禁用，发送也会被服务端拒绝）。
                        if (this.spotlightBlocked()) return;
                        const current = String(this.chatInput || '').trimEnd();
                        this.chatInput = current ? `${current}\n${text}` : text;
                        this.$nextTick(() => {
                            const input = document.getElementById('chatInputBox');
                            if (input) input.focus();
                        });
                    },

                    // NOTE: 在光标处插入「」并把光标放进括号内；有选中文字时直接包住选中内容。
                    insertDialogueQuotes() {
                        const input = document.getElementById('chatInputBox');
                        if (!input || input.disabled) return;
                        const value = String(this.chatInput || '');
                        const start = input.selectionStart ?? value.length;
                        const end = input.selectionEnd ?? value.length;
                        const selected = value.slice(start, end);
                        this.chatInput = value.slice(0, start) + '「' + selected + '」' + value.slice(end);
                        const caret = selected ? end + 2 : start + 1;
                        this.$nextTick(() => {
                            input.focus();
                            input.setSelectionRange(caret, caret);
                        });
                    },

                    splitAssistantContent(content) {
                        const kpMarkers = ['\n\nKP：', '\n\nKP:', '\nKP：', '\nKP:', 'KP：', 'KP:'];
                        let marker = '';
                        let splitAt = -1;
                        for (const m of kpMarkers) {
                            const idx = content.lastIndexOf(m);
                            if (idx > splitAt) {
                                splitAt = idx;
                                marker = m;
                            }
                        }

                        if (splitAt >= 0) {
                            const writerText = content.slice(0, splitAt).trim();
                            const narrationText = content.slice(splitAt + marker.length).trim();
                            if (narrationText) {
                                return { writerText, narrationText };
                            }
                        }

                        const sep = '\n\n';
                        splitAt = content.lastIndexOf(sep);
                        if (splitAt <= 0) {
                            return null;
                        }

                        const writerText = content.slice(0, splitAt).trim();
                        const narrationText = content.slice(splitAt + sep.length).trim();
                        if (!writerText || !narrationText) {
                            return null;
                        }

                        return { writerText, narrationText };
                    },

                    // NOTE: 重置等待状态到空初值，含新字段
                    resetWaitingInfo() {
                        this.waitingInfo = { pending: 0, total: 0, submitted_names: [], pending_names: [], batched: false, batch_user_ids: [], encounter_label: '', encounter_order: [] };
                    },

                    // NOTE: 判断调查员在当前轮的提交状态，供调查员列表 modal 展示 badge。
                    // 匹配时 trim 规范化，与后端 sessionPlayerDisplayName 逻辑一致。
                    // 返回 'submitted' | 'pending' | null
                    playerTurnSubmitState(player) {
                        if (!this.waitingForPlayers) return null;
                        const name = (player?.character_card?.name || player?.user?.username || '').trim();
                        if (!name) return null;
                        const submittedNames = (this.waitingInfo?.submitted_names || []).map(n => String(n).trim());
                        const pendingNames = (this.waitingInfo?.pending_names || []).map(n => String(n).trim());
                        if (submittedNames.includes(name)) return 'submitted';
                        if (pendingNames.includes(name)) return 'pending';
                        return null;
                    },

                    // NOTE: 从encounter_order(EncounterActor对象数组)取出当前批次里存活玩家的
                    // 名字并拼接，供等待横幅在战斗/追逐激活时提示"正在等谁行动"——同一批次可能
                    // 同时轮到多名DEX相邻的玩家。
                    encounterBatchActorNames() {
                        const order = this.waitingInfo?.encounter_order || [];
                        return order.filter(a => a && a.in_batch).map(a => a.name).join('、');
                    },

};

// ═══════════════════════════════════════════════════════════════
// DraggablePanel — Alpine component for mobile drag
// ═══════════════════════════════════════════════════════════════
window.draggablePanel = function() {
    const STORAGE_KEY = 'coc_drag_panel_pos';
    const DRAG_THRESHOLD = 8;

    function loadPosition() {
        try {
            const saved = localStorage.getItem(STORAGE_KEY);
            if (saved) {
                const p = JSON.parse(saved);
                if (typeof p.right === 'number' && typeof p.bottom === 'number') {
                    return p;
                }
            }
        } catch (_) {}
        return { right: 16, bottom: 24 };
    }

    function savePosition(pos) {
        try { localStorage.setItem(STORAGE_KEY, JSON.stringify(pos)); } catch (_) {}
    }

    function clamp(val, min, max) {
        return Math.max(min, Math.min(max, val));
    }

    return {
        pos: loadPosition(),
        isDragging: false,
        wasDragged: false,
        dragStartX: 0,
        dragStartY: 0,
        dragStartRight: 0,
        dragStartBottom: 0,
        movedDistance: 0,

        initDrag(el) {
            this.pos = loadPosition();
        },

        onTouchStart(e) {
            if (window.innerWidth >= 768) return;
            if (!e.touches || !e.touches.length) return;
            const t = e.touches[0];
            this.dragStartX = t.clientX;
            this.dragStartY = t.clientY;
            this.dragStartRight = this.pos.right;
            this.dragStartBottom = this.pos.bottom;
            this.isDragging = true;
            this.wasDragged = false;
            this.movedDistance = 0;
        },

        onTouchMove(e) {
            if (!this.isDragging || !e.touches || !e.touches.length) return;
            const t = e.touches[0];
            const dx = this.dragStartX - t.clientX;
            const dy = this.dragStartY - t.clientY;
            this.movedDistance = Math.abs(dx) + Math.abs(dy);
            if (this.movedDistance > DRAG_THRESHOLD) {
                this.wasDragged = true;
            }
            const vw = window.innerWidth;
            const vh = window.innerHeight;
            const panelW = 60;
            const panelH = 110;
            this.pos = {
                right: clamp(this.dragStartRight + dx, 0, vw - panelW),
                bottom: clamp(this.dragStartBottom + dy, 0, vh - panelH),
            };
        },

        onTouchEnd(e) {
            this.isDragging = false;
            savePosition(this.pos);
            const self = this;
            setTimeout(function() { self.wasDragged = false; }, 150);
        },

        onMouseDown(e) {
            if (window.innerWidth >= 768) return;
            this.dragStartX = e.clientX;
            this.dragStartY = e.clientY;
            this.dragStartRight = this.pos.right;
            this.dragStartBottom = this.pos.bottom;
            this.isDragging = true;
            this.wasDragged = false;
            this.movedDistance = 0;

            const self = this;
            function onMove(ev) {
                const dx = self.dragStartX - ev.clientX;
                const dy = self.dragStartY - ev.clientY;
                self.movedDistance = Math.abs(dx) + Math.abs(dy);
                if (self.movedDistance > DRAG_THRESHOLD) {
                    self.wasDragged = true;
                }
                const vw = window.innerWidth;
                const vh = window.innerHeight;
                const panelW = 60;
                const panelH = 110;
                self.pos = {
                    right: clamp(self.dragStartRight + dx, 0, vw - panelW),
                    bottom: clamp(self.dragStartBottom + dy, 0, vh - panelH),
                };
            }
            function onUp() {
                self.isDragging = false;
                savePosition(self.pos);
                document.removeEventListener('mousemove', onMove);
                document.removeEventListener('mouseup', onUp);
                setTimeout(function() { self.wasDragged = false; }, 150);
            }
            document.addEventListener('mousemove', onMove);
            document.addEventListener('mouseup', onUp);
        },
    };
};
