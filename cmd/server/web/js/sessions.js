// LLM-COC Sessions — Room management
window.COC = window.COC || {};
window.COC.sessions = {
                    // NOTE: 仅供加入游戏房间使用的候选人物卡：活跃、未死亡、HP>0
                    // 不修改全局 characters 数组，商城/复活列表不受影响
                    joinableCharacters() {
                        return (this.characters || []).filter(c =>
                            c.is_active !== false &&
                            c.wound_state !== 'dead' &&
                            (c.stats?.hp ?? 0) > 0
                        );
                    },
                    async loadSessions(page) {
                        const nextPage = Math.max(1, Number(page || this.sessionPage || 1));
                        const pageSize = Math.max(1, Number(this.sessionPageSize || 20));
                        this.sessionsLoading = true;
                        try {
                            const resp = await this.api('GET', `/api/sessions?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);
                            this.sessions = resp?.items || [];
                            this.sessionPage = Math.max(1, Number(resp?.page || nextPage));
                            this.sessionPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                            this.sessionTotal = Math.max(0, Number(resp?.total || 0));
                            this.sessionTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                        } finally { this.sessionsLoading = false; }
                    },
                    async prevSessionPage() {
                        if (this.sessionPage <= 1) return;
                        await this.loadSessions(this.sessionPage - 1);
                    },
                    async nextSessionPage() {
                        if (this.sessionPage >= this.sessionTotalPages) return;
                        await this.loadSessions(this.sessionPage + 1);
                    },
                    async loadMyHistory(page) {
                        const nextPage = Math.max(1, Number(page || this.myHistoryPage || 1));
                        const pageSize = Math.max(1, Number(this.myHistoryPageSize || 20));
                        const resp = await this.api('GET', `/api/sessions/my-history?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);
                        this.myHistory = resp?.items || [];
                        this.myHistoryPage = Math.max(1, Number(resp?.page || nextPage));
                        this.myHistoryPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                        this.myHistoryTotal = Math.max(0, Number(resp?.total || 0));
                        this.myHistoryTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                    },
                    async prevMyHistoryPage() {
                        if (this.myHistoryPage <= 1) return;
                        await this.loadMyHistory(this.myHistoryPage - 1);
                    },
                    async nextMyHistoryPage() {
                        if (this.myHistoryPage >= this.myHistoryTotalPages) return;
                        await this.loadMyHistory(this.myHistoryPage + 1);
                    },
                    async loadMyFavorites(page) {
                        const nextPage = Math.max(1, Number(page || this.myFavoritesPage || 1));
                        const pageSize = Math.max(1, Number(this.myFavoritesPageSize || 20));
                        const resp = await this.api('GET', `/api/sessions/my-favorites?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);
                        this.myFavorites = resp?.items || [];
                        this.myFavoritesPage = Math.max(1, Number(resp?.page || nextPage));
                        this.myFavoritesPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                        this.myFavoritesTotal = Math.max(0, Number(resp?.total || 0));
                        this.myFavoritesTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                    },
                    async prevMyFavoritesPage() {
                        if (this.myFavoritesPage <= 1) return;
                        await this.loadMyFavorites(this.myFavoritesPage - 1);
                    },
                    async nextMyFavoritesPage() {
                        if (this.myFavoritesPage >= this.myFavoritesTotalPages) return;
                        await this.loadMyFavorites(this.myFavoritesPage + 1);
                    },
                    async toggleFavorite(sessionId) {
                        try {
                            await this.api('POST', '/api/sessions/' + sessionId + '/favorite');
                            this.showToast('已收藏');
                            // 重新加载收藏列表
                            await this.loadMyFavorites(1);
                        } catch (e) {
                            this.showToast(e.message, 'error');
                        }
                    },
                    async removeFavorite(sessionId) {
                        try {
                            await this.api('DELETE', '/api/sessions/' + sessionId + '/favorite');
                            this.showToast('已取消收藏');
                            // 重新加载收藏列表；若当前页因删除清空则回退一页
                            const nextPage = this.myFavorites.length === 1 && this.myFavoritesPage > 1
                                ? this.myFavoritesPage - 1
                                : this.myFavoritesPage;
                            await this.loadMyFavorites(nextPage);
                        } catch (e) {
                            this.showToast(e.message, 'error');
                        }
                    },
                    async loadScenarios(page) {
                        const nextPage = Math.max(1, Number(page || this.scenarioPage || 1));
                        const pageSize = Math.max(1, Number(this.scenarioPageSize || 20));
                        const resp = await this.api('GET', `/api/scenarios?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);
                        this.scenarioList = resp?.items || [];
                        this.scenarioPage = Math.max(1, Number(resp?.page || nextPage));
                        this.scenarioPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                        this.scenarioTotal = Math.max(0, Number(resp?.total || 0));
                        this.scenarioTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                    },
                    async prevScenarioPage() {
                        if (this.scenarioPage <= 1) return;
                        await this.loadScenarios(this.scenarioPage - 1);
                    },
                    async nextScenarioPage() {
                        if (this.scenarioPage >= this.scenarioTotalPages) return;
                        await this.loadScenarios(this.scenarioPage + 1);
                    },
                    selectScenario(s) {
                        this.selectedScenario = s;
                        this.sessionForm.scenario_id = s.id;
                        if (!this.sessionForm.name) this.sessionForm.name = s.name;
                    },
                    // ══════════════════════════════════════════════════════════════════════
                    // Sessions
                    // ══════════════════════════════════════════════════════════════════════
                    async openCreateSession() {
                        this.selectedScenario = null;
                        this.sessionForm = { name: '', scenario_id: '', max_players: 4, password: '' };
                        this.modal = 'createSession';
                        await this.loadScenarios(1);
                    },

                    async createSession() {
                        if (!this.sessionForm.scenario_id) return;
                        // NOTE: 默认用剧本名作为房间名（选中项来自 selectedScenario 缓存，翻页后依然可用）
                        if (!this.sessionForm.name) {
                            this.sessionForm.name = this.selectedScenario?.name || '';
                        }
                        if (!this.sessionForm.name) return;
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
                        if (!await this.confirmDialog('确认退出该房间？', { danger: true, confirmText: '退出' })) return;
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
                            this.$nextTick(() => this.scrollChat(true));
                            this.showToast('游戏已开始！');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async endSession() {
                        const playerCount = this.currentSession?.players?.length || 0;
                        const costPerPlayer = this.shopCosts?.end_session_cost ?? 200;
                        const msg = playerCount > 1
                            ? `确认结束游戏？${playerCount}名玩家每人将消耗${costPerPlayer}金币（共${costPerPlayer * playerCount}金币），系统将进行成长评估。`
                            : `确认结束游戏？将消耗${costPerPlayer}金币，系统将进行成长评估。`;
                        if (!await this.confirmDialog(msg, { confirmText: '结束游戏' })) return;
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
                        if (!await this.confirmDialog(`确认复活房间「${session.name || session.id}」？`, { confirmText: '复活' })) return;
                        this.revivingSession = true;
                        try {
                            const r = await this.api('POST', '/api/sessions/' + session.id + '/revive');
                            await Promise.all([this.loadSessions(), this.loadMyHistory(), this.loadMyFavorites()]);
                            if (this.currentSession?.id === session.id) {
                                await this.refreshCurrentSession(true);
                                this.messages = this.normalizeMessages((await this.api('GET', '/api/sessions/' + session.id + '/messages')) || []);
                                this.$nextTick(() => this.scrollChat(true));
                            }
                            this.showToast(r.message || '房间已复活');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.revivingSession = false;
                    },

                    async reviveCurrentSession() {
                        await this.reviveSession(this.currentSession);
                    },

};
