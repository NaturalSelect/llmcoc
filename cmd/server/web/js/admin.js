// LLM-COC Admin — User/provider/agent/scenario management
window.COC = window.COC || {};
window.COC.admin = {
                    async loadAdminUsers() { this.adminUsers = (await this.api('GET', '/api/admin/users')) || []; },
                    async loadCacheStats() { this.cacheStats = await this.api('GET', '/api/admin/cache/stats'); },
                    async clearCache() {
                        if (!confirm('确认清空所有规则缓存？统计计数也将重置。')) return;
                        try {
                            await this.api('DELETE', '/api/admin/cache');
                            this.showToast('缓存已清空');
                            await Promise.all([this.loadCacheStats(), this.loadCacheKeys()]);
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },
                    async loadCacheKeys(page) {
                        const nextPage = Math.max(1, Number(page || this.cacheKeyPage || 1));
                        const pageSize = Math.max(1, Number(this.cacheKeyPageSize || 20));
                        try {
                            const resp = await this.api('GET', `/api/admin/cache/keys?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);
                            if (Array.isArray(resp)) {
                                // backward compat
                                this.cacheKeys = (resp || []).sort();
                                this.cacheKeyPage = 1;
                                this.cacheKeyTotal = this.cacheKeys.length;
                                this.cacheKeyTotalPages = 1;
                            } else {
                                this.cacheKeys = resp?.keys || [];
                                this.cacheKeyPage = Math.max(1, Number(resp?.page || nextPage));
                                this.cacheKeyPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                                this.cacheKeyTotal = Math.max(0, Number(resp?.total || 0));
                                this.cacheKeyTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                            }
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },
                    async setCacheKeyPage(page) {
                        const totalPages = Math.max(1, Number(this.cacheKeyTotalPages || 1));
                        const nextPage = Math.min(Math.max(1, Number(page || 1)), totalPages);
                        await this.loadCacheKeys(nextPage);
                    },
                    async prevCacheKeyPage() {
                        await this.setCacheKeyPage(this.cacheKeyPage - 1);
                    },
                    async nextCacheKeyPage() {
                        await this.setCacheKeyPage(this.cacheKeyPage + 1);
                    },
                    async deleteCacheEntry(key) {
                        if (!confirm('确认删除缓存条目：' + key + '？')) return;
                        try {
                            await this.api('DELETE', '/api/admin/cache/entry?key=' + encodeURIComponent(key));
                            this.showToast('条目已删除');
                            // 若当前页只剩1条且不是第1页，回退一页
                            if (this.cacheKeys.length <= 1 && this.cacheKeyPage > 1) {
                                await Promise.all([this.loadCacheStats(), this.loadCacheKeys(this.cacheKeyPage - 1)]);
                            } else {
                                await Promise.all([this.loadCacheStats(), this.loadCacheKeys()]);
                            }
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },
                    async loadAdminProviders() { this.adminProviders = (await this.api('GET', '/api/admin/config/providers')) || []; },
                    async loadAdminScenarios(page) {
                        const nextPage = Math.max(1, Number(page || this.adminScenarioPage || 1));
                        const pageSize = Math.max(1, Number(this.adminScenarioPageSize || 20));
                        const resp = await this.api('GET', `/api/admin/scenarios?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);

                        if (Array.isArray(resp)) {
                            this.adminScenarios = resp;
                            this.adminScenarioPage = nextPage;
                            this.adminScenarioPageSize = pageSize;
                            this.adminScenarioTotal = resp.length;
                            this.adminScenarioTotalPages = 1;
                            return;
                        }

                        this.adminScenarios = resp?.items || [];
                        this.adminScenarioPage = Math.max(1, Number(resp?.page || nextPage));
                        this.adminScenarioPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                        this.adminScenarioTotal = Math.max(0, Number(resp?.total || 0));
                        this.adminScenarioTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                    },
                    async setAdminScenarioPage(page) {
                        const totalPages = Math.max(1, Number(this.adminScenarioTotalPages || 1));
                        const nextPage = Math.min(Math.max(1, Number(page || 1)), totalPages);
                        await this.loadAdminScenarios(nextPage);
                    },
                    async prevAdminScenarioPage() {
                        await this.setAdminScenarioPage(this.adminScenarioPage - 1);
                    },
                    async nextAdminScenarioPage() {
                        await this.setAdminScenarioPage(this.adminScenarioPage + 1);
                    },
                    async changeAdminScenarioPageSize() {
                        const pageSize = Number(this.adminScenarioPageSize || 20);
                        this.adminScenarioPageSize = [10, 20, 50].includes(pageSize) ? pageSize : 20;
                        await this.loadAdminScenarios(1);
                    },
                    async loadAdminShopItems() { this.adminShopItems = (await this.api('GET', '/api/shop/items')) || []; },
                    async deleteScenario(id) {
                        if (!confirm('确认删除该模组？此操作不可逆。')) return;
                        try {
                            await this.api('DELETE', '/api/scenarios/' + id);
                            this.showToast('模组已删除');
                            const nextPage = this.adminScenarios.length === 1 && this.adminScenarioPage > 1
                                ? this.adminScenarioPage - 1
                                : this.adminScenarioPage;
                            await Promise.all([this.loadAdminScenarios(nextPage), this.loadScenarios()]);
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },
                    async loadAdminAgents() {
                        const agents = (await this.api('GET', '/api/admin/config/agents')) || [];
                        agents.forEach(ag => { if (ag.provider_config_id == null) ag.provider_config_id = ''; });

                        // Ensure all configurable roles exist in UI, even if DB row is missing.
                        const roleDefaults = {
                            director: { max_tokens: 1500, temperature: 0.7 },
                            writer: { max_tokens: 800, temperature: 0.85 },
                            lawyer: { max_tokens: 800, temperature: 0.3 },
                            npc: { max_tokens: 600, temperature: 0.9 },
                            evaluator: { max_tokens: 1200, temperature: 0.5 },
                            growth: { max_tokens: 1000, temperature: 0.4 },
                            architect: { max_tokens: 4000, temperature: 0.7 },
                            qa_guard: { max_tokens: 2000, temperature: 0.3 },
                            parser: { max_tokens: 4000, temperature: 0.1 },
                        };
                        const expectedRoles = Object.keys(roleDefaults).filter(r => r !== 'scripter');
                        const byRole = new Map(agents.filter(a => a.role !== 'scripter').map(a => [a.role, a]));

                        agents.splice(0, agents.length, ...byRole.values());

                        for (const role of expectedRoles) {
                            if (byRole.has(role)) continue;
                            const d = roleDefaults[role] || { max_tokens: 1024, temperature: 0.7 };
                            agents.push({
                                id: 'new-' + role,
                                role,
                                provider_config_id: '',
                                model_name: '',
                                max_tokens: d.max_tokens,
                                temperature: d.temperature,
                                system_prompt: '',
                                is_active: true,
                            });
                        }

                        this.adminAgents = agents;
                    },
                    // ═══════════════════════════════════════════════════════
                    // Admin
                    // ═══════════════════════════════════════════════════════
                    async adminRecharge() {
                        try {
                            const r = await this.api('POST', '/api/admin/recharge', this.rechargeForm);
                            this.showToast(`充值成功：${r.user.username} +${this.rechargeForm.amount} 金币`);
                            await this.loadAdminUsers();
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async toggleAdmin(u) {
                        const newRole = u.role === 'admin' ? 'user' : 'admin';
                        if (!confirm(`将 ${u.username} 设为 ${newRole}？`)) return;
                        try {
                            await this.api('PUT', '/api/admin/users/' + u.id + '/role', { role: newRole });
                            u.role = newRole; this.showToast('角色已更新');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async toggleBan(u) {
                        if (u.is_banned) {
                            if (!confirm(`解封 ${u.username}？`)) return;
                            try {
                                await this.api('PUT', '/api/admin/users/' + u.id + '/unban');
                                u.is_banned = false; u.ban_reason = '';
                                this.showToast('已解封');
                            } catch (e) { this.showToast(e.message, 'error'); }
                        } else {
                            const reason = prompt(`封号原因（可留空）：`);
                            if (reason === null) return;
                            try {
                                await this.api('PUT', '/api/admin/users/' + u.id + '/ban', { reason });
                                u.is_banned = true; u.ban_reason = reason;
                                this.showToast('已封号');
                            } catch (e) { this.showToast(e.message, 'error'); }
                        }
                    },

                    openCreateProvider() {
                        this.editingProvider = null;
                        this.providerForm = { name: '', provider: 'openai', base_url: '', api_key: '', is_active: true };
                        this.modal = 'provider';
                    },
                    openEditProvider(p) {
                        this.editingProvider = p;
                        this.providerForm = { name: p.name, provider: p.provider, base_url: p.base_url, api_key: '', is_active: p.is_active };
                        this.modal = 'provider';
                    },

                    async saveProvider() {
                        this.loading = true;
                        try {
                            if (this.editingProvider) {
                                await this.api('PUT', '/api/admin/config/providers/' + this.editingProvider.id, this.providerForm);
                                this.showToast('提供商已更新');
                            } else {
                                await this.api('POST', '/api/admin/config/providers', this.providerForm);
                                this.showToast('提供商已创建');
                            }
                            this.modal = null; await this.loadAdminProviders();
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    async deleteProvider(id) {
                        if (!confirm('确认删除此提供商？')) return;
                        try {
                            await this.api('DELETE', '/api/admin/config/providers/' + id);
                            this.showToast('已删除'); await this.loadAdminProviders();
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async pingProvider(id) {
                        this.pingLoading = id;
                        try {
                            const r = await this.api('POST', '/api/admin/config/providers/' + id + '/ping', {});
                            this.showToast(`连通正常，延迟 ${r.latency_ms} ms`);
                        } catch (e) { this.showToast('连接失败：' + e.message, 'error'); }
                        this.pingLoading = null;
                    },

                    async saveAgent(ag) {
                        try {
                            await this.api('PUT', '/api/admin/config/agents/' + ag.role, {
                                provider_config_id: ag.provider_config_id || null,
                                model_name: ag.model_name,
                                max_tokens: ag.max_tokens,
                                temperature: ag.temperature,
                                system_prompt: ag.system_prompt,
                                thinking_level: ag.thinking_level || '',
                                is_active: ag.is_active,
                            });
                            this.showToast(this.agentLabel(ag.role) + ' 配置已保存');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async adminCreateShopItem() {
                        if (!this.newShopItem.name) return;
                        try {
                            await this.api('POST', '/api/admin/shop/items', this.newShopItem);
                            this.showToast('商品已创建');
                            this.newShopItem = { name: '', description: '', item_type: 'card_slot', price: 0, value: 1, is_active: true };
                            await this.loadAdminShopItems();
                            this.shopItems = this.adminShopItems.slice();
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async adminDeleteShopItem(item) {
                        if (!item?.id) return;
                        if (!confirm('确认删除商品“' + item.name + '”？删除后将从商城下架。')) return;
                        try {
                            await this.api('DELETE', '/api/admin/shop/items/' + item.id);
                            this.showToast('商品已删除');
                            await this.loadAdminShopItems();
                            this.shopItems = this.adminShopItems.slice();
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    // ── Site settings & invite codes ─────────────────────────────────────
                    async loadSiteSettings() {
                        const settings = (await this.api('GET', '/api/admin/config/settings')) || [];
                        const map = {};
                        settings.forEach(s => map[s.key] = s.value);
                        this.siteSettings.require_invite_code = map.require_invite_code === 'true';
                    },
                    async toggleInviteCodeSetting() {
                        const newVal = this.siteSettings.require_invite_code ? 'false' : 'true';
                        try {
                            await this.api('PUT', '/api/admin/config/settings/require_invite_code', { value: newVal });
                            this.siteSettings.require_invite_code = newVal === 'true';
                            this.showToast('设置已更新');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },
                    async loadInviteCodes() {
                        this.inviteCodes = (await this.api('GET', '/api/admin/invite-codes')) || [];
                    },
                    async generateInviteCodes() {
                        try {
                            await this.api('POST', '/api/admin/invite-codes', { count: this.inviteCodeCount || 5 });
                            this.showToast('邀请码已生成');
                            await this.loadInviteCodes();
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },
                    async deleteInviteCode(id) {
                        if (!confirm('确认删除该邀请码？')) return;
                        try {
                            await this.api('DELETE', '/api/admin/invite-codes/' + id);
                            this.showToast('已删除');
                            await this.loadInviteCodes();
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async downloadScenarioTemplate() {
                        try {
                            const resp = await fetch('/api/scenarios/template', {
                                method: 'GET',
                                headers: { ...(this.token ? { 'Authorization': 'Bearer ' + this.token } : {}) },
                            });
                            if (!resp.ok) {
                                const data = await resp.json().catch(() => ({}));
                                throw new Error(data.error || `HTTP ${resp.status}`);
                            }
                            const blob = await resp.blob();
                            const url = URL.createObjectURL(blob);
                            const a = document.createElement('a');
                            a.href = url;
                            a.download = 'scenario_template.json';
                            document.body.appendChild(a);
                            a.click();
                            a.remove();
                            URL.revokeObjectURL(url);
                            this.showToast('模板下载成功');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    onScenarioFilePicked(e) {
                        const f = e?.target?.files?.[0] || null;
                        this.scenarioUploadFile = f;
                    },

                    async uploadScenarioFile() {
                        if (!this.scenarioUploadFile) return;
                        this.loading = true;
                        try {
                            const form = new FormData();
                            form.append('file', this.scenarioUploadFile);
                            const resp = await fetch('/api/scenarios/upload', {
                                method: 'POST',
                                headers: { ...(this.token ? { 'Authorization': 'Bearer ' + this.token } : {}) },
                                body: form,
                            });
                            const data = await resp.json().catch(() => ({}));
                            if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
                            this.scenarioUploadFile = null;
                            this.showToast('模组导入成功');
                            await Promise.all([this.loadScenarios(), this.loadAdminScenarios(1)]);
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    async generateScenarioByAgents() {
                        const minPlayers = Number(this.scenarioGenForm.min_players || 1);
                        const maxPlayers = Number(this.scenarioGenForm.max_players || 4);
                        if (maxPlayers < minPlayers) {
                            this.showToast('最大玩家数不能小于最小玩家数', 'error');
                            return;
                        }
                        this.loading = true;
                        try {
                            await this.api('POST', '/api/scenarios/generate', {
                                name: this.scenarioGenForm.name || '',
                                theme: this.scenarioGenForm.theme || '',
                                era: this.scenarioGenForm.era || '',
                                brief: this.scenarioGenForm.brief || '',
                                target_length: this.scenarioGenForm.target_length || 'short',
                                min_players: minPlayers,
                                max_players: maxPlayers,
                                difficulty: this.scenarioGenForm.difficulty || 'normal',
                            });
                            this.showToast('AI 模组生成并入库成功');
                            await Promise.all([this.loadScenarios(), this.loadAdminScenarios(1)]);
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    viewScenario(s) { this.viewingScenario = s; this.modal = 'scenarioDetail'; },
                    async viewScenarioGenerationLog(s) {
                        if (!s?.id) return;
                        this.scenarioGenerationLog = { scenario_id: s.id, scenario_name: s.name || '', has_log: false, log_text: '' };
                        this.scenarioGenerationLogLoading = true;
                        this.modal = 'scenarioGenerationLog';
                        try {
                            const resp = await this.api('GET', '/api/admin/scenarios/' + s.id + '/generation-log');
                            this.scenarioGenerationLog = resp || this.scenarioGenerationLog;
                        } catch (e) {
                            this.showToast(e.message, 'error');
                            this.modal = null;
                        }
                        this.scenarioGenerationLogLoading = false;
                    },
                    slotToTime(slot) {
                        if (!slot && slot !== 0) return '未设置';
                        const h = Math.floor(slot / 2);
                        const m = (slot % 2) * 30;
                        return String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0');
                    },

                    agentLabel(role) {
                        return {
                            director: '🎬 Director', writer: '✍️ Writer', lawyer: '⚖️ Lawyer',
                            npc: '🎭 NPC', evaluator: '📊 Evaluator', growth: '🌱 Growth',
                            architect: '🏗️ Architect', qa_guard: '🔍 QA Guard',
                            parser: '🔧 Parser',
                        }[role] || role;
                    },
                    agentDesc(role) {
                        return {
                            director: '场景导演 — 决策工具调用序列',
                            writer: '叙事撰写 — 生成面向玩家的克苏鲁叙述（流式输出）',
                            lawyer: '规则顾问 — 查阅规则书提供裁决依据',
                            npc: 'NPC扮演 — 给出场景NPC的行动与对话',
                            evaluator: '成长评估 — 分析本场表现建议奖励',
                            growth: '成长应用 — 将评估结果写入角色卡',
                            architect: '模组设计 — 生成大纲并转化为完整JSON模组',
                            qa_guard: '质量审查 — 审查模组可玩性、规则合规与线索设计',
                            parser: 'JSON修复 — 低温度结构化输出，修复其他Agent的JSON格式错误',
                        }[role] || '';
                    },
                    agentDefaultHint(role) {
                        return {
                            director: '留空使用内置 Director 提示词（JSON工具调用格式）',
                            writer: '留空使用内置 Writer 提示词（克苏鲁散文风格）',
                            lawyer: '留空使用内置 Lawyer 提示词（规则查阅模式）',
                            npc: '留空使用内置 NPC 提示词',
                            evaluator: '留空使用内置 Evaluator 提示词',
                            growth: '留空使用内置 Growth 提示词',
                            architect: '留空使用内置 Architect 提示词（大纲+JSON生成）',
                            qa_guard: '留空使用内置 QA Guard 提示词（模组质量审查）',
                            parser: '留空使用内置 Parser 提示词（JSON格式修复）',
                        }[role] || '留空使用默认';
                    },

    // Load admin page HTML template on demand.
    async loadAdminPage() {
        if (this.adminHTML) return;
        try {
            const resp = await fetch('./pages/admin.html');
            if (resp.ok) this.adminHTML = await resp.text();
        } catch (_) {}
    },
};
