// LLM-COC Game — Chat, SSE streaming, auto-refresh
window.COC = window.COC || {};
window.COC.game = {
                    // ═══════════════════════════════════════════════════════
                    // Chat / SSE
                    // ═══════════════════════════════════════════════════════
                    async sendChat() {
                        const content = this.chatInput.trim();
                        if (!content || this.streaming || this.waitingForPlayers) return;

                        this.chatInput = '';
                        this.streaming = true;
                        this.writerBuffer = '';
                        this.narrationBuffer = '';

                        const submittedAt = new Date();
                        this.preSendMessageCount = this.messages.length;
                        this.messages.push({
                            id: Date.now(), role: 'user',
                            username: this.currentPlayerDisplayName(), content,
                            created_at: submittedAt.toISOString(),
                            _new: true,
                        });
                        this.$nextTick(() => this.scrollChat());

                        const formData = new FormData();
                        formData.append('content', content);

                        let receivedWaiting = false;
                        let receivedDone = false;

                        const abortCtrl = new AbortController();
                        const abortTimer = setTimeout(() => abortCtrl.abort(), 10 * 60 * 1000);

                        try {
                            const resp = await fetch('/api/sessions/' + this.currentSession.id + '/chat', {
                                method: 'POST',
                                headers: { 'Authorization': 'Bearer ' + this.token },
                                body: formData,
                                signal: abortCtrl.signal,
                            });

                            if (!resp.ok) {
                                const err = await resp.json().catch(() => ({ error: '请求失败' }));
                                this.showToast(err.error || '发送失败', 'error');
                                this.streaming = false;
                                return;
                            }

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
                                        switch (currentEvent) {

                                            case 'thinking':
                                                break;

                                            case 'token':
                                                if (data) {
                                                    this.writerBuffer += data;
                                                    this.$nextTick(() => this.scrollChat());
                                                }
                                                break;

                                            case 'narration':
                                                if (data) {
                                                    this.narrationBuffer += data;
                                                    this.$nextTick(() => this.scrollChat());
                                                }
                                                break;

                                            case 'waiting':
                                                receivedWaiting = true;
                                                try {
                                                    const info = JSON.parse(data);
                                                    if (info.pending !== undefined) this.waitingInfo = info;
                                                } catch (_) { }
                                                break;

                                            case 'error':
                                                this.showToast(data || '处理失败', 'error');
                                                break;

                                            case 'done':
                                                receivedDone = true;
                                                if (!receivedWaiting && (this.writerBuffer || this.narrationBuffer)) {
                                                    this._commitStreamBuffers();
                                                }
                                                break;
                                        }
                                    } else if (line === '') {
                                        currentEvent = '';
                                    }
                                }
                            }
                        } catch (e) {
                            if (e.name !== 'AbortError') {
                                this.showToast(e.message || '网络错误', 'error');
                            }
                        } finally {
                            clearTimeout(abortTimer);
                            this.streaming = false;
                        }

                        if (receivedWaiting) {
                            this.waitingForPlayers = true;
                            this.waitingSince = submittedAt;
                            this.pollForKPResponse(submittedAt);
                        } else if (receivedDone) {
                            if (this.writerBuffer || this.narrationBuffer) {
                                this._commitStreamBuffers();
                            }
                        } else {
                            if (this.writerBuffer || this.narrationBuffer) {
                                this._commitStreamBuffers();
                            } else {
                                this._recoverFromDrop(submittedAt);
                            }
                        }
                    },

                    async _recoverFromDrop(since) {
                        if (!this.currentSession?.id || this.page !== 'game') return;
                        await new Promise(r => setTimeout(r, this.refreshIntervals.recoveryPoll));
                        try {
                            const msgs = this.normalizeMessages(
                                (await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []
                            );
                            const kp = msgs.find(m => m.role === 'assistant' && new Date(m.created_at) > since);
                            if (kp) {
                                await this.refreshCurrentSession(true);
                                await this.refreshCurrentMessages(true);
                                return;
                            }
                        } catch (_) { }

                        if (this.currentSession?.id && this.page === 'game') {
                            this._recoverFromDrop(since);
                        }
                    },

                    _commitStreamBuffers() {
                        const wt = this.stripAckContent(this.writerBuffer).trim();
                        const nt = this.stripAckContent(this.narrationBuffer).trim();
                        const fullContent = wt + (nt ? '\n\nKP:' + nt : '');
                        this.messages.push({
                            id: Date.now() + 1,
                            role: 'assistant', username: 'KP',
                            content: fullContent,
                            writer_text: wt,
                            narration_text: nt,
                            created_at: new Date().toISOString(),
                            _new: true,
                        });
                        this.writerBuffer = '';
                        this.narrationBuffer = '';
                        this.refreshCurrentSession(true).catch(() => { });
                        this.$nextTick(() => this.scrollChat());
                        // Sync tail from DB so all players' actions appear in correct order.
                        // In multi-player the last submitter only has their own action locally;
                        // DB (already written before 'done' SSE) has every player's action.
                        this._syncTailFromDB();
                    },

                    async _syncTailFromDB() {
                        if (!this.currentSession?.id || this.preSendMessageCount < 0) return;
                        const anchor = this.preSendMessageCount;
                        this.preSendMessageCount = -1;
                        // Reuse refreshingMessages guard to prevent concurrent refreshCurrentMessages
                        // from running an incremental append while we do the full splice.
                        if (this.refreshingMessages) return;
                        this.refreshingMessages = true;
                        try {
                            const msgs = this.normalizeMessages(
                                (await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []
                            );
                            if (msgs.length > anchor) {
                                // Replace everything from anchor onwards with the DB-canonical tail
                                this.messages.splice(
                                    anchor,
                                    this.messages.length - anchor,
                                    ...msgs.slice(anchor).map(m => ({ ...m, _new: true }))
                                );
                                this.$nextTick(() => this.scrollChat());
                            }
                        } catch (_) { }
                        finally {
                            this.refreshingMessages = false;
                        }
                    },

                    async pollForKPResponse(since) {
                        if (!this.waitingForPlayers) return;
                        try {
                            const msgs = this.normalizeMessages((await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []);
                            const newKP = msgs.find(m => m.role === 'assistant' && new Date(m.created_at) > since);
                            if (newKP) {
                                this.waitingForPlayers = false;
                                this.waitingSince = null;
                                this.waitingInfo = { pending: 0, total: 0 };
                                await this.refreshCurrentSession(true);
                                await this.refreshCurrentMessages(true);
                                return;
                            }
                        } catch (_) { }
                        setTimeout(() => this.pollForKPResponse(since), this.refreshIntervals.waitingPoll);
                    },

                    scrollChat() {
                        const el = document.getElementById('chatBox');
                        if (!el) return;
                        const threshold = 120;
                        const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold;
                        if (atBottom) el.scrollTop = el.scrollHeight;
                    },

                    async refreshCurrentSession(silent = true) {
                        if (!this.currentSession?.id || this.refreshingSession) return;
                        this.refreshingSession = true;
                        try {
                            this.currentSession = await this.api('GET', '/api/sessions/' + this.currentSession.id);
                        } catch (e) {
                            if (!silent) this.showToast(e.message, 'error');
                        } finally {
                            this.refreshingSession = false;
                        }
                    },

                    async refreshCurrentMessages(silent = true) {
                        if (!this.currentSession?.id || this.refreshingMessages) return;
                        this.refreshingMessages = true;
                        try {
                            const msgs = this.normalizeMessages(
                                (await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []
                            );

                            // 增量追加：只取比当前多出来的新消息，避免全量替换导致滚动位置重置
                            const newMsgs = msgs.slice(this.messages.length).map(m => ({ ...m, _new: true }));
                            if (newMsgs.length > 0) {
                                this.messages.push(...newMsgs);
                                if (this.waitingForPlayers && this.waitingSince) {
                                    const gotKP = newMsgs.some(m => m.role === 'assistant' && new Date(m.created_at) > this.waitingSince);
                                    if (gotKP) {
                                        this.waitingForPlayers = false;
                                        this.waitingSince = null;
                                        this.waitingInfo = { pending: 0, total: 0 };
                                    }
                                }
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

                    normalizeMessages(messages) {
                        return messages.map(msg => this.normalizeMessage(msg));
                    },

                    normalizeMessage(message) {
                        if (!message || message.role !== 'assistant') {
                            return message;
                        }

                        message = {
                            ...message,
                            content: this.stripAckContent(message.content || ''),
                            writer_text: this.stripAckContent(message.writer_text || ''),
                            narration_text: this.stripAckContent(message.narration_text || ''),
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

                    stripAckContent(content) {
                        return String(content || '')
                            .replace(/<ack\b[^>]*>[\s\S]*?<\/ack>/gi, '')
                            .replace(/<ack\b[^>]*>[\s\S]*$/gi, '')
                            .trim();
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

                    // ═══════════════════════════════════════════════════════
                    // Character card helper for draggable panel
                    // ═══════════════════════════════════════════════════════
                    getMyGameChar() {
                        const uid = this.user?.id;
                        const player = this.currentSession?.players?.find(p => p.user_id === uid || p.user?.id === uid);
                        return player?.character_card || null;
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
            const panelW = 230;
            const panelH = Math.min(350, vh * 0.55);
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
                const panelW = 230;
                const panelH = Math.min(350, vh * 0.55);
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

        onBtnTouch(e, callback) {
            if (this.wasDragged) {
                e.preventDefault();
                return;
            }
            const self = this;
            function onEnd(ev) {
                if (!self.wasDragged && typeof callback === 'function') {
                    callback();
                }
                document.removeEventListener('touchend', onEnd);
            }
            document.addEventListener('touchend', onEnd, { once: true });
        },
    };
};
