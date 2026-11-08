// LLM-COC Game — 房间 WebSocket 连接、命令与门控、消息渲染辅助
// NOTE: 状态归约全部在 room_state.js（纯逻辑，有 node 测试）；这里只负责连接、命令和模板辅助。
// 页面里没有任何轮询定时器：房间进展只来自 WebSocket，断线后靠重连和快照收敛。
window.COC = window.COC || {};
(function () {
    const RS = window.COC.roomState;
    const BACKOFF = [1000, 2000, 4000, 8000, 15000];
    const STARTING_TIMEOUT = 10000;
    const CHAT_MAX_CHARS = 2000;
    const STATUSES = ['lobby', 'playing', 'ended'];
    const STOP_TEXT = {
        host_stop: '房主打断',
        loop: '检测到重复操作',
        stall: '长时间没有进展',
        error: '出错',
        context: '上下文过长',
    };

    // NOTE: 连接句柄和定时器放在模块级，不放进 Alpine 响应式状态：WebSocket 被 Proxy 包裹后行为不可靠。
    const conn = {
        ws: null,
        gen: 0,            // 每次建立连接 +1，旧连接的回调和迟到的快照响应据此丢弃
        enterGen: 0,       // 每次进入或离开房间 +1，丢弃迟到的进房请求
        timer: null,       // 重连退避
        backoff: 0,
        failsBeforeHello: 0,
        sessionTimer: null,
        startTimer: null,
        scrollQueued: false,
        wake: null,
    };

    window.COC.game = {
        // ═══════════════════════════════════════════════════════
        // 房间生命周期与连接
        // ═══════════════════════════════════════════════════════
        async enterRoom(id) {
            this.leaveRoom();
            const enterGen = conn.enterGen;
            let session;
            try {
                session = await this.api('GET', '/api/sessions/' + id);
            } catch (e) {
                this.showToast(e.message, 'error');
                return;
            }
            if (enterGen !== conn.enterGen) return;

            const isHost = session.created_by === this.user?.id || this.user?.role === 'admin';
            const room = RS.newRoom({ sessionId: session.id, userId: this.user?.id, isHost });
            if (STATUSES.includes(session.status)) room.status = session.status;
            this.currentSession = session;
            this.room = room;
            this.chatInput = '';
            this.hudFlash = { hp: false, san: false };
            this._hudLastStats = null;
            this._syncHudFlash(this.myCurrentCard());
            this.page = 'game';
            this._wsBindWake();
            this._wsConnect();
        },

        leaveRoom() {
            conn.enterGen++;
            conn.gen++;
            clearTimeout(conn.timer);
            clearTimeout(conn.sessionTimer);
            clearTimeout(conn.startTimer);
            conn.timer = conn.sessionTimer = conn.startTimer = null;
            conn.backoff = 0;
            conn.failsBeforeHello = 0;
            if (conn.ws) {
                try { conn.ws.close(1000); } catch (_) { }
                conn.ws = null;
            }
            if (conn.wake) {
                document.removeEventListener('visibilitychange', conn.wake);
                window.removeEventListener('online', conn.wake);
                conn.wake = null;
            }
            this.room = null;
        },

        // NOTE: 页面重新可见或网络恢复时，不等退避定时器，立刻重连。
        _wsBindWake() {
            if (conn.wake) return;
            conn.wake = () => {
                if (document.visibilityState === 'hidden') return;
                const room = this.room;
                if (!room) return;
                if (conn.ws && (conn.ws.readyState === WebSocket.OPEN || conn.ws.readyState === WebSocket.CONNECTING)) return;
                conn.backoff = 0;
                this._wsConnect();
            };
            document.addEventListener('visibilitychange', conn.wake);
            window.addEventListener('online', conn.wake);
        },

        _wsConnect() {
            const room = this.room;
            if (!room) return;
            const gen = ++conn.gen;
            clearTimeout(conn.timer);
            conn.timer = null;
            if (conn.ws) {
                try { conn.ws.close(1000); } catch (_) { }
                conn.ws = null;
            }

            const proto = location.protocol === 'https:' ? 'wss' : 'ws';
            let url = proto + '://' + location.host + '/api/sessions/' + room.sessionId + '/ws?token=' + encodeURIComponent(this.token);
            if (room.lastSeq > 0) url += '&since=' + room.lastSeq;
            if (room.epoch) url += '&epoch=' + encodeURIComponent(room.epoch);

            let ws;
            try {
                ws = new WebSocket(url);
            } catch (_) {
                this._wsScheduleReconnect(room);
                return;
            }
            conn.ws = ws;
            let gotHello = false;

            ws.onmessage = (ev) => {
                if (conn.gen !== gen || this.room !== room) return;
                let frame;
                try { frame = JSON.parse(ev.data); } catch (_) { return; }
                if (!frame || typeof frame.type !== 'string') return;

                if (frame.type === 'hello') {
                    gotHello = true;
                    conn.backoff = 0;
                    conn.failsBeforeHello = 0;
                    this._runEffects(room, RS.onHello(room, frame), gen);
                    return;
                }
                const verdict = RS.acceptFrame(room, frame);
                if (verdict === 'apply') {
                    this._runEffects(room, RS.applyFrame(room, frame), gen);
                    this._afterFrame(room, frame);
                } else if (verdict === 'resync') {
                    this._wsResync(room);
                }
            };

            ws.onclose = () => {
                if (conn.gen !== gen || this.room !== room) return;
                conn.ws = null;
                RS.markDisconnected(room);
                // NOTE: 浏览器拿不到握手失败的状态码；没等到 hello 就断开时，主动探测是登录失效、被封禁、没有权限还是房间没了。
                // 首次连接（还没有 epoch）一次失败就探测，重连场景连续三次才探测，避免网络抖动时频繁请求。
                if (!gotHello && ++conn.failsBeforeHello >= (room.epoch ? 3 : 1)) {
                    conn.failsBeforeHello = 0;
                    this._probeRoom(room).then((ok) => {
                        if (ok && this.room === room && conn.gen === gen) this._wsScheduleReconnect(room);
                    });
                    return;
                }
                this._wsScheduleReconnect(room);
            };
        },

        _wsScheduleReconnect(room) {
            if (this.room !== room) return;
            const delay = BACKOFF[Math.min(conn.backoff, BACKOFF.length - 1)];
            conn.backoff++;
            clearTimeout(conn.timer);
            conn.timer = setTimeout(() => {
                conn.timer = null;
                if (this.room === room) this._wsConnect();
            }, delay);
        },

        // 本地状态可能缺事件（出现序号缺口、服务端要求 resync）：丢弃游标，立即重连并重新拉快照。
        _wsResync(room) {
            RS.resetCursor(room);
            conn.backoff = 0;
            this._wsConnect();
        },

        async _probeRoom(room) {
            try {
                await this.api('GET', '/api/auth/me');
            } catch (e) {
                if (this.room !== room) return false;
                if (e.status === 401) {
                    this.showToast('登录已失效，请重新登录', 'error');
                    this.logout();
                    return false;
                }
            }
            // NOTE: GET /sessions/:id 对所有登录用户开放，有密码房间的权限只在 /messages 和 WebSocket 握手里检查，
            // 所以用 /messages 探测房间是否存在、当前用户是否有权查看。
            try {
                await this.api('GET', '/api/sessions/' + room.sessionId + '/messages');
            } catch (e) {
                if (this.room !== room) return false;
                if (e.status === 404 || e.status === 403) {
                    this.showToast(e.message, 'error');
                    this.goTo('sessions');
                    return false;
                }
            }
            return this.room === room;
        },

        _runEffects(room, effects, gen) {
            for (const effect of effects || []) {
                if (effect === 'fetchSnapshot') this._fetchSnapshot(room, gen);
                else if (effect === 'fetchSession') this._scheduleSessionFetch(room);
                else if (effect === 'fetchChatStatus') this._fetchChatStatus(room);
            }
        },

        async _fetchSnapshot(room, gen) {
            const base = '/api/sessions/' + room.sessionId;
            try {
                const [session, messages, chatStatus] = await Promise.all([
                    this.api('GET', base),
                    this.api('GET', base + '/messages'),
                    this.api('GET', base + '/chat-status'),
                ]);
                // NOTE: 拉取期间换了房间或重连过，这份快照已经过期，丢弃；新连接的 hello 会重新触发快照。
                if (this.room !== room || conn.gen !== gen) return;
                this.currentSession = session;
                const effects = RS.applySnapshot(room, { session, messages: messages || [], chatStatus });
                this._runEffects(room, effects, gen);
                this._syncHudFlash(this.myCurrentCard());
                this.$nextTick(() => this.scrollChat(true));
            } catch (e) {
                if (this.room !== room || conn.gen !== gen) return;
                if (e.status === 401) {
                    this.showToast('登录已失效，请重新登录', 'error');
                    this.logout();
                    return;
                }
                if (e.status === 403 || e.status === 404) {
                    this.showToast(e.message, 'error');
                    this.goTo('sessions');
                    return;
                }
                // 其他错误：关掉连接走重连，重连后的 hello 会重新触发快照。
                if (conn.ws) {
                    try { conn.ws.close(); } catch (_) { }
                }
            }
        },

        _scheduleSessionFetch(room) {
            if (conn.sessionTimer) return;
            conn.sessionTimer = setTimeout(() => {
                conn.sessionTimer = null;
                if (this.room === room) this.refreshCurrentSession(true);
            }, 300);
        },

        async refreshCurrentSession(silent = true) {
            const id = this.currentSession?.id;
            if (!id) return;
            try {
                const session = await this.api('GET', '/api/sessions/' + id);
                if (this.currentSession?.id !== id) return;
                this.currentSession = session;
                if (this.room && STATUSES.includes(session.status)) this.room.status = session.status;
                this._syncHudFlash(this.myCurrentCard());
            } catch (e) {
                if (!silent) this.showToast(e.message, 'error');
                else if (e.status === 404 && this.room && this.currentSession?.id === id) {
                    this.showToast('房间已不存在', 'error');
                    this.goTo('sessions');
                }
            }
        },

        // NOTE: chat-status 只用来校正发言权、等待进度和"我是否已提交"。请求期间如果又有事件到达，响应可能比事件旧，
        // 直接套用会把刚收到的周期状态盖回去，所以重拉一次而不是应用。
        async _fetchChatStatus(room, attempt = 0) {
            const seq = room.lastSeq;
            try {
                const cs = await this.api('GET', '/api/sessions/' + room.sessionId + '/chat-status');
                if (this.room !== room) return;
                if (room.lastSeq !== seq) {
                    if (attempt < 2) await this._fetchChatStatus(room, attempt + 1);
                    return;
                }
                RS.applyChatStatus(room, cs);
            } catch (_) { }
        },

        _afterFrame(room, frame) {
            if (frame.type === 'message_new') {
                const m = room.byId[frame.data?.message?.id];
                if (m) m._new = true;
            }
            if (frame.type === 'message_new' || frame.type === 'message_update' || frame.type === 'writer_token') {
                this._queueScroll();
            }
        },

        _queueScroll() {
            if (conn.scrollQueued) return;
            conn.scrollQueued = true;
            requestAnimationFrame(() => {
                conn.scrollQueued = false;
                this.scrollChat();
            });
        },

        // 已在底部附近才跟随滚动，避免打断用户翻阅历史；force 用于首次进入和自己发言后。
        scrollChat(force = false) {
            const el = document.getElementById('chatBox');
            if (!el) return;
            const threshold = 120;
            const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
            if (force || atBottom) el.scrollTop = el.scrollHeight;
        },

        // ═══════════════════════════════════════════════════════
        // 门控与命令
        // ═══════════════════════════════════════════════════════
        // NOTE: 方法而不是 getter：init.js 用 Object.assign 合并各模块，getter 会在合并时被求值成静态值。
        gates() {
            if (!this.room) return { canSpeak: false, canContinue: false, canStop: false, live: false };
            return RS.gates(this.room);
        },

        async sendChat() {
            const room = this.room;
            if (!room) return false;
            const text = String(this.chatInput).trim();
            if (!text || !RS.gates(room).canSpeak) return false;
            if ([...text].length > CHAT_MAX_CHARS) {
                this.showToast('消息过长（最多 ' + CHAT_MAX_CHARS + ' 字）', 'error');
                return false;
            }

            // NOTE: /chat 只读表单字段 content，不收 JSON，所以不能用 api()。
            const form = new FormData();
            form.append('content', text);
            let resp;
            try {
                resp = await fetch('/api/sessions/' + room.sessionId + '/chat', {
                    method: 'POST',
                    headers: { 'Authorization': 'Bearer ' + this.token },
                    body: form,
                });
            } catch (_) {
                this.showToast('网络错误，请重试', 'error');
                return false;
            }
            const data = await resp.json().catch(() => ({}));
            if (this.room !== room) return false;

            if (resp.status === 202) {
                // NOTE: 不插入乐观消息。点名的人到齐、周期开始后，自己的行动才会经 message_new 落进列表。
                room.me.submitted = true;
                room.me.draft = text;
                this.chatInput = '';
                return true;
            }
            this.showToast(data.error || '发送失败', 'error');
            if (resp.status === 401) {
                this.logout();
            } else if (resp.status === 409 || resp.status === 403) {
                this._fetchChatStatus(room);
            }
            return false;
        },

        // 把已提交的内容放回输入框，再次提交时服务端会覆盖之前的内容。
        editSubmitted() {
            const room = this.room;
            if (!room || !room.me.draft) return;
            this.chatInput = room.me.draft;
            this.$nextTick(() => {
                const input = document.getElementById('chatInputBox');
                if (input) input.focus();
            });
        },

        // 房主点击「让 KP 继续」：有被点名但未回应的玩家时先确认。
        async hostContinueKP() {
            const room = this.room;
            if (!room || !RS.gates(room).canContinue) return false;
            if (room.spotlight.userIds.length > 0) {
                if (!await this.confirmDialog('跳过未回应的玩家并让 KP 继续？', { confirmText: '让 KP 继续' })) return false;
                if (this.room !== room || !RS.gates(room).canContinue) return false;
            }
            return this._postKPTurn(room);
        },

        async _postKPTurn(room, { quiet = false } = {}) {
            room.floor.starting = true;
            clearTimeout(conn.startTimer);
            // NOTE: 点了继续却一直没收到 floor_start（请求被拒或事件丢了）时，不能让按钮永远处于启动中。
            conn.startTimer = setTimeout(() => {
                conn.startTimer = null;
                if (this.room === room && room.floor.starting) {
                    room.floor.starting = false;
                    this._fetchChatStatus(room);
                }
            }, STARTING_TIMEOUT);
            try {
                await this.api('POST', '/api/sessions/' + room.sessionId + '/kp-turn');
                return true;
            } catch (e) {
                if (this.room !== room) return false;
                room.floor.starting = false;
                clearTimeout(conn.startTimer);
                conn.startTimer = null;
                if (!(quiet && e.status === 409)) this.showToast(e.message, 'error');
                if (e.status === 409) this._fetchChatStatus(room);
                return false;
            }
        },

        async hostStop() {
            const room = this.room;
            if (!room || !RS.gates(room).canStop) return false;
            if (!await this.confirmDialog('打断 KP？本周期已发布的内容会保留。', { danger: true, confirmText: '打断' })) return false;
            if (this.room !== room) return false;
            try {
                await this.api('POST', '/api/sessions/' + room.sessionId + '/kp-stop');
            } catch (e) {
                if (this.room !== room) return false;
                if (e.status === 409) {
                    this.showToast('KP 当前没有在发言', 'error');
                    this._fetchChatStatus(room);
                } else {
                    this.showToast(e.message, 'error');
                }
                return false;
            }
            // NOTE: 202 之后什么都不做，等 floor_end(host_stop) 推送过来。
            return true;
        },

        // ═══════════════════════════════════════════════════════
        // 横幅、占位符与按钮文案
        // ═══════════════════════════════════════════════════════
        // tone: warn 连接问题 | me 轮到我 | info 其他进行中状态 | idle 不显示横幅，只给占位符用
        roomBanner() {
            const room = this.room;
            if (!room) return { icon: '', text: '', tone: 'idle' };
            const g = RS.gates(room);
            if (!g.live) {
                const text = room.conn === 'reconnecting' ? '连接中断，正在重连…'
                    : room.conn === 'snapshot' ? '正在同步房间状态…' : '正在连接…';
                return { icon: '🔄', text, tone: 'warn' };
            }
            if (room.status === 'ended') return { icon: '', text: '游戏已结束', tone: 'idle' };
            if (room.status !== 'playing') return { icon: '', text: '等待房主开始游戏', tone: 'idle' };
            const sp = room.spotlight;
            if (room.floor.running) return { icon: '🎭', text: room.kpStatus || 'KP 正在推进剧情…', tone: 'info' };
            if (room.floor.starting) return { icon: '🎭', text: 'KP 正在启动…', tone: 'info' };
            if (sp.stop) {
                const why = STOP_TEXT[sp.stop] || sp.stop;
                return { icon: '⏸', text: 'KP 已暂停：' + why + '，等待房主让 KP 继续', tone: 'info' };
            }
            const names = (sp.names || []).join('、');
            if (sp.askedMe) {
                if (!room.me.submitted) {
                    return { icon: '👉', text: sp.question ? 'KP 在等你回应：' + sp.question : 'KP 在等你回应，请写下你的行动', tone: 'me' };
                }
                const waitFor = (room.waiting.pendingNames || []).join('、');
                return { icon: '⏳', text: waitFor ? '已提交行动，还在等 ' + waitFor : '已提交行动，等待 KP', tone: 'info' };
            }
            if (sp.userIds.length > 0) {
                const w = room.waiting;
                const progress = w.total > 1 ? '（' + (w.total - w.pending) + '/' + w.total + ' 已提交）' : '';
                return { icon: '⏳', text: 'KP 在等 ' + (names || '玩家') + ' 回应' + progress, tone: 'info' };
            }
            return { icon: '🎭', text: this.hasKPStarted() ? '等待房主让 KP 继续' : '等待房主让 KP 开场', tone: 'info' };
        },

        chatPlaceholder() {
            const normal = '以角色身份写下你的行动或台词，如：我推开门，「有人在吗？」 (Ctrl+Enter 发送)';
            const room = this.room;
            if (!room) return normal;
            if (RS.gates(room).canSpeak) {
                return room.spotlight.question ? 'KP 问你：' + room.spotlight.question + '（Ctrl+Enter 发送）' : normal;
            }
            return this.roomBanner().text || normal;
        },

        sendLabel() {
            const room = this.room;
            if (!room) return '发送';
            const g = RS.gates(room);
            if (g.canSpeak) return room.me.submitted ? '重新提交' : '发送';
            if (!g.live) return '连接中';
            if (room.status !== 'playing') return '不可发言';
            if (room.floor.running) return 'KP 发言中';
            return '等待点名';
        },

        hasKPStarted() {
            return !!this.room && this.room.messages.some((m) => m.floor > 0);
        },

        continueLabel() {
            return this.hasKPStarted() ? '让 KP 继续' : '让 KP 开场';
        },

        showWaiting() {
            const r = this.room;
            return !!r && r.status === 'playing' && !r.floor.running
                && r.waiting.total > 1 && r.waiting.pending > 0 && r.waiting.pending < r.waiting.total;
        },

        // NOTE: 判断调查员在当前轮的提交状态，供调查员列表弹窗展示标记。
        // 匹配时 trim 规范化，与后端 sessionPlayerDisplayName 逻辑一致。返回 'submitted' | 'pending' | null
        playerTurnSubmitState(player) {
            if (!this.showWaiting()) return null;
            const name = (player?.character_card?.name || player?.user?.username || '').trim();
            if (!name) return null;
            if ((this.room.waiting.submittedNames || []).some((n) => String(n).trim() === name)) return 'submitted';
            if ((this.room.waiting.pendingNames || []).some((n) => String(n).trim() === name)) return 'pending';
            return null;
        },

        chatCharCount() {
            return [...String(this.chatInput || '')].length;
        },

        // ═══════════════════════════════════════════════════════
        // 消息渲染辅助
        // ═══════════════════════════════════════════════════════
        // 旧数据没有 kind：assistant 当作一整块 KP 回复，其余按 role 归类。
        msgKind(msg) {
            if (msg?.kind) return msg.kind;
            if (msg?.role === 'assistant') return 'legacy';
            if (msg?.role === 'user') return 'player';
            return 'system';
        },

        msgClass(msg) {
            const kind = this.msgKind(msg);
            const fade = msg._new ? 'fade-up ' : '';
            if (kind === 'player') return fade + 'msg-user' + (this.isMyMessage(msg) ? ' msg-user-self' : '');
            if (kind === 'system') return fade + 'msg-sys';
            return fade + 'msg-kp';
        },

        msgLabel(msg) {
            const kind = this.msgKind(msg);
            if (kind === 'player') return msg.username || '玩家';
            if (kind === 'system') return '系统';
            return 'KP';
        },

        isMyMessage(msg) {
            return this.msgKind(msg) === 'player' && !!msg.user_id && Number(msg.user_id) === Number(this.user?.id);
        },

        // pending 的 writer 显示已流式收到的部分；其余状态直接用落库的正文。
        writerText(msg) {
            if (msg.status === 'pending') return this.room?.streams?.[msg.id]?.text || '';
            return msg.content || '';
        },

        diceOf(msg) {
            return RS.parseDice(msg?.meta?.dice);
        },

        timeOf(msg) {
            return RS.parseTimePoint(msg?.meta?.time_point);
        },

        currentGameTime() {
            const list = this.room?.messages || [];
            for (let i = list.length - 1; i >= 0; i--) {
                const tp = list[i]?.meta?.time_point;
                if (tp) return RS.parseTimePoint(tp);
            }
            return null;
        },

        // NOTE: 只给没有 kind 的旧消息用：把历史上混在正文里的内部标签清掉。
        stripLegacyTags(content) {
            content = String(content || '');
            if (!content.includes('<')) return content.trim();
            return content
                .replace(/<image_ref\b[^>]*(?:\/>|>\s*<\/image_ref>)/gi, '')
                .replace(/<(ack|direction|response_options|writer_pending|image_data_url|dice|time_point)\b[^>]*>[\s\S]*?<\/\1>/gi, '')
                .replace(/<(ack|direction|response_options|writer_pending|image_data_url|dice|time_point)\b[^>]*>[\s\S]*$/gi, '')
                .trim();
        },

        // 只在被点名时给出推荐行动，取最近一条 KP 回复里的选项；之后出现过我自己的行动就不再推荐。
        activeResponseSuggestions() {
            const room = this.room;
            if (!room || !RS.gates(room).canSpeak) return null;
            for (let i = room.messages.length - 1; i >= 0; i--) {
                const msg = room.messages[i];
                if (msg.kind === 'player') return null;
                if (msg.kind !== 'kp') continue;
                const options = msg.meta?.options;
                return Array.isArray(options) && options.length > 0 ? { options } : null;
            }
            return null;
        },

        appendResponseSuggestion(option) {
            const text = String(option || '').trim();
            if (!text || !this.gates().canSpeak) return;
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

        // ═══════════════════════════════════════════════════════
        // 角色状态条
        // ═══════════════════════════════════════════════════════
        myCurrentCard() {
            const uid = this.user?.id;
            if (!uid) return null;
            return this.currentSession?.players?.find((p) => p.user_id === uid)?.character_card || null;
        },

        statPercent(value, max) {
            const total = Number(max) || 0;
            if (total <= 0) return 0;
            return Math.max(0, Math.min(100, Math.round((Number(value) || 0) / total * 100)));
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
    };
})();

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
