// LLM-COC Sessions — Room management
window.COC = window.COC || {};
window.COC.sessions = {
                    async loadSessions() { this.sessions = (await this.api('GET', '/api/sessions')) || []; },
                    async loadMyHistory() { this.myHistory = (await this.api('GET', '/api/sessions/my-history')) || []; },
                    async loadMyFavorites() { this.myFavorites = (await this.api('GET', '/api/sessions/my-favorites')) || []; },
                    async toggleFavorite(sessionId) {
                        try {
                            await this.api('POST', '/api/sessions/' + sessionId + '/favorite');
                            this.showToast('已收藏');
                            // 重新加载收藏列表
                            await this.loadMyFavorites();
                        } catch (e) {
                            this.showToast(e.message, 'error');
                        }
                    },
                    async removeFavorite(sessionId) {
                        try {
                            await this.api('DELETE', '/api/sessions/' + sessionId + '/favorite');
                            this.showToast('已取消收藏');
                            // 重新加载收藏列表
                            await this.loadMyFavorites();
                        } catch (e) {
                            this.showToast(e.message, 'error');
                        }
                    },
                    async loadScenarios() { this.scenarioList = (await this.api('GET', '/api/scenarios')) || []; },
                    // ══════════════════════════════════════════════════════════════════════
                    // Sessions
                    // ══════════════════════════════════════════════════════════════════════
                    async openCreateSession() {
                        await this.loadScenarios();
                        this.sessionForm = { name: '', scenario_id: '', max_players: 4, password: '' };
                        this.modal = 'createSession';
                    },

                    async createSession() {
                        if (!this.sessionForm.name || !this.sessionForm.scenario_id) return;
                        this.loading = true;
                        try {
                            const s = await this.api('POST', '/api/sessions', this.sessionForm);
                            this.sessions.unshift(s); this.modal = null; this.showToast('房间创建成功！');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    openJoinSession(s) {
                        this.joinTargetSession = s;
                        this.joinForm = { character_card_id: '', password: '' };
                        this.modal = 'joinSession';
                    },

                    async joinSession() {
                        if (!this.joinForm.character_card_id) return;
                        this.loading = true;
                        try {
                            await this.api('POST', '/api/sessions/' + this.joinTargetSession.id + '/join', this.joinForm);
                            this.modal = null; this.showToast('加入成功！');
                            await this.loadSessions();
                            // If there's an active game session, refresh its players list immediately
                            if (this.currentSession?.id) {
                                await this.refreshCurrentSession(true);
                            }
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    async leaveSession(session) {
                        if (!session?.id) return;
                        if (!confirm('确认退出该房间？')) return;
                        this.leavingSession = true;
                        try {
                            const r = await this.api('POST', '/api/sessions/' + session.id + '/leave');
                            this.showToast(r.message || '已退出房间');
                            await this.loadSessions();
                            if (this.currentSession?.id === session.id) {
                                this.currentSession = null;
                                this.messages = [];
                                this.goTo('sessions');
                            }
                        } catch (e) {
                            this.showToast(e.message, 'error');
                        }
                        this.leavingSession = false;
                    },

                    async leaveCurrentSession() {
                        if (!this.currentSession?.id) return;
                        await this.leaveSession(this.currentSession);
                    },

                    isInSession(s) { return s.players?.some(p => p.user_id === this.user?.id); },

                    async startSession() {
                        try {
                            await this.api('POST', '/api/sessions/' + this.currentSession.id + '/start');
                            await this.refreshCurrentSession(true);
                            this.messages = this.normalizeMessages((await this.api('GET', '/api/sessions/' + this.currentSession.id + '/messages')) || []);
                            this.$nextTick(() => this.scrollChat());
                            this.showToast('游戏已开始！');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async endSession() {
                        const playerCount = this.currentSession?.players?.length || 0;
                        const costPerPlayer = 200;
                        const msg = playerCount > 1
                            ? `确认结束游戏？${playerCount}名玩家每人将消耗${costPerPlayer}金币（共${costPerPlayer * playerCount}金币），系统将进行成长评估。`
                            : `确认结束游戏？将消耗${costPerPlayer}金币，系统将进行成长评估。`;
                        if (!confirm(msg)) return;
                        this.endingSession = true;
                        try {
                            const r = await this.api('POST', '/api/sessions/' + this.currentSession.id + '/end');
                            await this.refreshCurrentSession(true);
                            this.showToast(r.message || '游戏已结束，角色成长已结算。');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.endingSession = false;
                    },

                    async reviveSession(session) {
                        if (!session?.id) return;
                        if (!confirm(`确认复活房间「${session.name || session.id}」？`)) return;
                        this.revivingSession = true;
                        try {
                            const r = await this.api('POST', '/api/sessions/' + session.id + '/revive');
                            await Promise.all([this.loadSessions(), this.loadMyHistory(), this.loadMyFavorites()]);
                            if (this.currentSession?.id === session.id) {
                                await this.refreshCurrentSession(true);
                                this.messages = this.normalizeMessages((await this.api('GET', '/api/sessions/' + session.id + '/messages')) || []);
                                this.$nextTick(() => this.scrollChat());
                            }
                            this.showToast(r.message || '房间已复活');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.revivingSession = false;
                    },

                    async reviveCurrentSession() {
                        await this.reviveSession(this.currentSession);
                    },

};
