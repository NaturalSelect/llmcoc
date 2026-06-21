// LLM-COC Game — Chat, SSE streaming, auto-refresh
window.COC = window.COC || {};
window.COC.game = {
                    // ═══════════════════════════════════════════════════════
                    // Chat / SSE
                    // ═══════════════════════════════════════════════════════
                    async sendChat() {
                        const content = String(this.chatInput).trim();
                        if (!content || this.streaming || this.waitingForPlayers) return false;

                        const streamID = Date.now() + ':' + Math.random().toString(16).slice(2);
                        const unlockKP = () => {
                            if (this.activeStreamID === streamID) {
                                this.streaming = false;
                                this.activeStreamID = null;
                            }
                        };

                        this.chatInput = '';
                        this.streaming = true;
                        this.activeStreamID = streamID;
                        this.writerBuffer = '';
                        this.narrationBuffer = '';
                        this.imageBuffer = [];
                        this.progressText = '已收到行动,正在整理本轮信息';

                        const submittedAt = new Date();
                        const tailAnchor = this.messages.length;
                        this.preSendMessageCount = tailAnchor;
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
                        let committedKP = false;
                        let assistantMessageID = null;
                        let writerText = '';

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
                                unlockKP();
                                return false;
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
                                                if (!this.progressText) {
                                                    this.progressText = 'Agent 团队运行中，请稍候…';
                                                }
                                                break;

                                            case 'progress':
                                                this.progressText = data || this.progressText;
                                                break;

                                            case 'token':
                                                if (data) {
                                                    if (assistantMessageID) {
                                                        writerText += data;
                                                        this._appendWriterToMessage(assistantMessageID, writerText);
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
                                                if (data && data.startsWith('data:image/')) {
                                                    if (assistantMessageID) {
                                                        this._appendImageToMessage(assistantMessageID, data);
                                                    } else {
                                                        this.imageBuffer.push(data);
                                                    }
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

                                            case 'kp_done':
                                                try {
                                                    const info = JSON.parse(data || '{}');
                                                    assistantMessageID = info.message_id || assistantMessageID;
                                                    const msg = this._commitStreamBuffers({
                                                        id: assistantMessageID,
                                                        created_at: info.created_at,
                                                        writerStreaming: !!info.has_writer && !info.writer_done,
                                                        anchor: tailAnchor,
                                                    });
                                                    if (msg) assistantMessageID = msg.id;
                                                    committedKP = !!msg;
                                                } catch (_) {
                                                    const msg = this._commitStreamBuffers({ anchor: tailAnchor });
                                                    if (msg) assistantMessageID = msg.id;
                                                    committedKP = !!msg;
                                                }
                                                unlockKP();
                                                break;

                                            case 'done':
                                                receivedDone = true;
                                                this.progressText = '';
                                                if (assistantMessageID) {
                                                    this._setMessageWriterDone(assistantMessageID);
                                                }
                                                if (!committedKP && !receivedWaiting && (this.writerBuffer || this.narrationBuffer || this.imageBuffer.length)) {
                                                    const msg = this._commitStreamBuffers({ anchor: tailAnchor });
                                                    if (msg) assistantMessageID = msg.id;
                                                    committedKP = !!msg;
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
                            this.progressText = '';
                        } finally {
                            clearTimeout(abortTimer);
                            unlockKP();
                        }

                        if (receivedWaiting) {
                            this.writerBuffer = '';
                            this.narrationBuffer = '';
                            this.imageBuffer = [];
                            this.progressText = '';
                            this.waitingForPlayers = true;
                            this.waitingSince = submittedAt;
                            this.pollForKPResponse(submittedAt);
                        } else if (receivedDone && !committedKP) {
                            if (this.writerBuffer || this.narrationBuffer || this.imageBuffer.length) {
                                this._commitStreamBuffers({ anchor: tailAnchor });
                            }
                        } else if (!committedKP) {
                            if (this.writerBuffer || this.narrationBuffer || this.imageBuffer.length) {
                                this._commitStreamBuffers({ anchor: tailAnchor });
                            } else {
                                this._recoverFromDrop(submittedAt);
                            }
                        }
                        return true;
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

                    _assistantContent(writerText, narrationText) {
                        const wt = this.stripAckContent(writerText).trim();
                        const nt = this.stripAckContent(narrationText).trim();
                        if (!nt) return wt;
                        return (wt ? wt + '\n\n' : '') + 'KP:' + nt;
                    },

                    _commitStreamBuffers(meta = {}) {
                        const responseOptions = this.extractResponseOptionsPayload(this.narrationBuffer) || this.extractResponseOptionsPayload(this.writerBuffer);
                        const wt = this.stripAckContent(this.writerBuffer).trim();
                        const nt = this.stripAckContent(this.narrationBuffer).trim();
                        const images = Array.isArray(this.imageBuffer) ? [...this.imageBuffer] : [];
                        if (!wt && !nt && images.length === 0) return null;
                        const msg = {
                            id: meta.id || Date.now() + 1,
                            role: 'assistant', username: 'KP',
                            content: this._assistantContent(wt, nt),
                            writer_text: wt,
                            narration_text: nt,
                            images,
                            response_options: responseOptions,
                            created_at: meta.created_at || new Date().toISOString(),
                            _writer_streaming: !!meta.writerStreaming,
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

                    _appendImageToMessage(messageID, dataURL) {
                        const idx = this.messages.findIndex(m => String(m.id) === String(messageID));
                        if (idx < 0) {
                            if (!this.imageBuffer.includes(dataURL)) {
                                this.imageBuffer.push(dataURL);
                            }
                            return;
                        }
                        const msg = this.messages[idx];
                        const images = Array.isArray(msg.images) ? [...msg.images] : [];
                        if (!images.includes(dataURL)) {
                            images.push(dataURL);
                        }
                        this.messages.splice(idx, 1, { ...msg, images });
                    },

                    async _syncTailFromDB(anchor = null) {
                        if (!this.currentSession?.id) return;
                        if (anchor === null || anchor === undefined) {
                            if (this.preSendMessageCount < 0) return;
                            anchor = this.preSendMessageCount;
                        }
                        if (this.preSendMessageCount === anchor) {
                            this.preSendMessageCount = -1;
                        }
                        // 复用刷新锁,避免常规轮询在这里做尾部替换时同时追加消息。
                        if (this.refreshingMessages) return;
                        this.refreshingMessages = true;
                        try {
                            const msgs = this.normalizeMessages(
                                (await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []
                            );
                            if (msgs.length > anchor) {
                                const localByID = new Map(this.messages.map(m => [String(m.id), m]));
                                const tail = msgs.slice(anchor).map(m => {
                                    const local = localByID.get(String(m.id));
                                    return { ...this._mergeStreamingMessage(local, m), _new: true };
                                });
                                // 用数据库尾部替换本地尾部,修正多人行动顺序。
                                this.messages.splice(
                                    anchor,
                                    this.messages.length - anchor,
                                    ...tail
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

                            const existingByID = new Map(this.messages.map((m, idx) => [String(m.id), idx]));
                            const newMsgs = [];
                            let changed = false;

                            for (const msg of msgs) {
                                const idx = existingByID.get(String(msg.id));
                                if (idx === undefined) {
                                    newMsgs.push({ ...msg, _new: true });
                                    continue;
                                }
                                const old = this.messages[idx];
                                const merged = this._mergeStreamingMessage(old, msg);
                                if (
                                    old.content !== merged.content ||
                                    old.writer_text !== merged.writer_text ||
                                    old.narration_text !== merged.narration_text ||
                                    !this.sameImages(old.images, merged.images) ||
                                    old._writer_streaming !== merged._writer_streaming
                                ) {
                                    this.messages.splice(idx, 1, {
                                        ...old,
                                        ...merged,
                                        _new: old._new,
                                        response_options: merged.response_options,
                                    });
                                    changed = true;
                                }
                            }

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
                            }
                            if (changed || newMsgs.length > 0) {
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

                    _mergeStreamingMessage(local, fresh) {
                        if (!local || local.role !== 'assistant' || fresh.role !== 'assistant') {
                            return fresh;
                        }
                        const localImages = Array.isArray(local.images) ? local.images : [];
                        const freshImages = Array.isArray(fresh.images) ? fresh.images : [];
                        const mergedImages = [...freshImages];
                        for (const image of localImages) {
                            if (!mergedImages.includes(image)) {
                                mergedImages.push(image);
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
                                if (typeof image === 'string' && image.startsWith('data:image/') && !images.includes(image)) {
                                    images.push(image);
                                }
                            }
                        }
                        if (images.length === 0 && String(content).includes('<image_data_url')) {
                            images.push(...this.extractImageDataURLs(content));
                        }
                        message = {
                            ...message,
                            content: this.stripAckContent(content),
                            writer_text: this.stripAckContent(message.writer_text || ''),
                            narration_text: this.stripAckContent(message.narration_text || ''),
                            images,
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
                            if (dataURL.startsWith('data:image/') && !images.includes(dataURL)) {
                                images.push(dataURL);
                            }
                        }
                        return images;
                    },

                    stripAckContent(content) {
                        content = String(content || '');
                        if (!content.includes('<ack') &&
                            !content.includes('<direction') &&
                            !content.includes('<response_options') &&
                            !content.includes('<writer_pending') &&
                            !content.includes('<image_data_url')) {
                            return content.trim();
                        }
                        return content
                            .replace(/<(ack|direction|response_options|writer_pending|image_data_url)\b[^>]*>[\s\S]*?<\/\1>/gi, '')
                            .replace(/<(ack|direction|response_options|writer_pending|image_data_url)\b[^>]*>[\s\S]*$/gi, '')
                            .trim();
                    },

                    activeResponseSuggestions() {
                        for (let i = this.messages.length - 1; i >= 0; i--) {
                            const msg = this.messages[i];
                            if (msg.role === 'user') return null;
                            if (
                                msg.role === 'assistant' &&
                                msg.response_options &&
                                Array.isArray(msg.response_options.options) &&
                                msg.response_options.options.length > 0
                            ) {
                                return msg.response_options;
                            }
                        }
                        return null;
                    },

                    appendResponseSuggestion(option) {
                        const text = String(option || '').trim();
                        if (!text) return;
                        const current = String(this.chatInput || '').trimEnd();
                        this.chatInput = current ? `${current}\n${text}` : text;
                        this.$nextTick(() => {
                            const input = document.getElementById('chatInputBox');
                            if (input) input.focus();
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
