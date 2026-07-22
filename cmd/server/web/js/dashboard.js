// LLM-COC Dashboard — Character management
window.COC = window.COC || {};
window.COC.dashboard = {
                    async loadCharacters() {
                        this.dashboardLoading = true;
                        try {
                            this.characters = await this.fetchAllPages('/api/characters', {
                                onProgress: items => { this.characters = items; },
                            });
                        } finally { this.dashboardLoading = false; }
                    },
                    async loadDeadCharacters() {
                        this.deadCharacters = await this.fetchAllPages('/api/characters/dead', {
                            onProgress: items => { this.deadCharacters = items; },
                        });
                    },
                    // ══════════════════════════════════════════════════════════════════════
                    // Characters
                    // ══════════════════════════════════════════════════════════════════════
                    openCreateChar() {
                        this.createCharMode = 'ai';
                        this.manualStep = 1;
                        this.manualDraft = null;
                        this.manualSkillDefaults = {};
                        this.manualSkills = {};
                        this.manualSkillBudget = { occupation: 0, interest: 0, total: 0 };
                        this.manualSkillNames = [];
                        this.charForm = { name: '', race: '人类', age: 25, gender: '', occupation: '', birthplace: '', residence: '', backstory: '', appearance: '', traits: '', creation_hint: '' };
                        this.genForm = { name: '', race: '人类', gender: '', age: '', occupation: '', era: '', background: '' };
                        this.loadSkillDefaults().catch(() => {});
                        this.modal = 'createChar';
                    },
                    openCharDetail(c) { this.editChar = c; this.inventoryInput = ''; this.modal = 'charDetail'; },
                    openSessionPlayerCharDetail(player) {
                        if (!player?.character_card) {
                            this.showToast('该玩家未绑定人物卡', 'error');
                            return;
                        }
                        if (player.user_id !== this.user?.id && this.user?.role !== 'admin') {
                            this.showToast('只能查看自己的角色详细信息', 'error');
                            return;
                        }
                        this.openCharDetail(player.character_card);
                    },
                    openCurrentSessionCharDetail() {
                        const uid = this.user?.id;
                        const p = this.currentSession?.players?.find(player => player.user_id === uid);
                        if (p?.character_card) {
                            this.openCharDetail(p.character_card);
                            return;
                        }
                        this.showToast('未找到当前房间绑定的人物卡', 'error');
                    },

                    async loadSkillDefaults() {
                        const r = await this.api('GET', '/api/characters/skill-defaults');
                        this.manualSkillDefaults = r.skill_defaults || {};
                        if (this.manualDraft?.stats) {
                            this.manualSkillDefaults['母语'] = this.manualDraft.stats.edu || 0;
                            this.manualSkillDefaults['闪避'] = Math.floor((this.manualDraft.stats.dex || 0) / 2);
                        }
                        this.manualSkillNames = (r.skill_names || Object.keys(this.manualSkillDefaults)).filter(n => n !== '克苏鲁神话');
                    },

                    async rollManualChar() {
                        const age = Number(this.charForm.age || 0);
                        if (age < 15 || age > 90) {
                            this.showToast('年龄必须在15-90之间', 'error');
                            return;
                        }
                        this.loading = true;
                        try {
                            const r = await this.api('POST', '/api/characters/roll', { age });
                            this.manualDraft = r;
                            this.manualSkillDefaults = r.skill_defaults || this.manualSkillDefaults || {};
                            this.manualSkillDefaults['母语'] = r.stats?.edu || this.manualSkillDefaults['母语'] || 0;
                            this.manualSkillDefaults['闪避'] = Math.floor((r.stats?.dex || 0) / 2);
                            this.manualSkillBudget = r.skill_budget || { occupation: 0, interest: 0, total: 0 };
                            const names = this.manualSkillNames.length ? this.manualSkillNames : Object.keys(this.manualSkillDefaults).sort();
                            this.manualSkillNames = names.filter(n => n !== '克苏鲁神话');
                            this.manualSkills = { ...this.manualSkillDefaults };
                            this.manualSkills['母语'] = r.stats?.edu || this.manualSkills['母语'] || 0;
                            this.manualSkills['闪避'] = Math.floor((r.stats?.dex || 0) / 2);
                            this.manualStep = 2;
                            this.showToast('属性已锁定，请继续填写调查员资料');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    manualSpentPoints() {
                        const defaults = this.manualSkillDefaults || {};
                        const skills = this.manualSkills || {};
                        return Object.keys(skills).reduce((sum, name) => {
                            if (name === '母语' || name === '闪避' || name === '克苏鲁神话') return sum;
                            const base = Number(defaults[name] ?? 0);
                            const val = Number(skills[name] ?? base);
                            return sum + Math.max(0, val - base);
                        }, 0);
                    },

                    manualRemainingPoints() {
                        return Number(this.manualSkillBudget?.total || 0) - this.manualSpentPoints();
                    },

                    clampManualSkill(name) {
                        const base = Number(this.manualSkillDefaults?.[name] ?? 0);
                        if (name === '母语') {
                            this.manualSkills[name] = this.manualDraft?.stats?.edu || base;
                            return;
                        }
                        if (name === '闪避') {
                            this.manualSkills[name] = Math.floor((this.manualDraft?.stats?.dex || 0) / 2);
                            return;
                        }
                        let val = Number(this.manualSkills[name] ?? base);
                        if (!Number.isFinite(val)) val = base;
                        if (val < base) val = base;
                        if (val > 90) val = 90;
                        this.manualSkills[name] = Math.floor(val);
                    },

                    async finalizeManualChar() {
                        if (!this.manualDraft?.draft_id || !this.manualDraft?.token) {
                            this.showToast('请先投掷并锁定属性', 'error');
                            return;
                        }
                        if (!this.charForm.name.trim()) {
                            this.showToast('姓名不能为空', 'error');
                            return;
                        }
                        if (!['男', '女'].includes(this.charForm.gender)) {
                            this.showToast('请选择性别', 'error');
                            return;
                        }
                        if (this.manualRemainingPoints() < 0) {
                            this.showToast('技能点已超出预算', 'error');
                            return;
                        }
                        this.loading = true;
                        try {
                            const c = await this.api('POST', '/api/characters/finalize', {
                                draft_id: this.manualDraft.draft_id,
                                token: this.manualDraft.token,
                                name: this.charForm.name,
                                gender: this.charForm.gender,
                                occupation: this.charForm.occupation,
                                birthplace: this.charForm.birthplace,
                                residence: this.charForm.residence,
                                creation_hint: this.charForm.creation_hint || this.charForm.backstory,
                                skills: this.manualSkills,
                            });
                            this.characters.push(c); this.modal = null;
                            await this.loadMe(); this.showToast('规则车卡创建成功，AI背景已生成！');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    async generateChar() {
                        this.loading = true;
                        try {
                            const c = await this.api('POST', '/api/characters/generate', this.genForm);
                            this.characters.push(c); this.modal = null;
                            await this.loadMe(); this.showToast('AI 调查员生成成功！');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.loading = false;
                    },

                    async deleteChar(id) {
                        if (!await this.confirmDialog('确认删除此人物卡？', { danger: true, confirmText: '删除' })) return;
                        try {
                            await this.api('DELETE', '/api/characters/' + id);
                            this.characters = this.characters.filter(c => c.id !== id);
                            await this.loadDeadCharacters();
                            this.showToast('人物卡已删除');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async deleteDeadCharacter(char) {
                        if (!await this.confirmDialog(`确认彻底删除「${char.name}」？此操作不可撤销，角色将永久消逝。`, { danger: true, confirmText: '彻底删除' })) return;
                        try {
                            await this.api('DELETE', '/api/characters/' + char.id + '/dead');
                            this.deadCharacters = this.deadCharacters.filter(c => c.id !== char.id);
                            this.showToast(`${char.name} 已彻底消逝……`);
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async reviveCharacter(char) {
                        if (!await this.confirmDialog(`确认花费 ${this.shopCosts?.revive_base_cost ?? 2000} 金币复活「${char.name}」？\n复活后HP为最大值的一半，随机失去一半装备，遗忘一半法术。`, { confirmText: '复活' })) return;
                        this.reviving = true;
                        try {
                            const r = await this.api('POST', '/api/characters/' + char.id + '/revive');
                            if (this.user) this.user.coins = r.coins;
                            this.characters.push(r.character_card);
                            this.deadCharacters = this.deadCharacters.filter(c => c.id !== char.id);
                            this.showToast(`${char.name} 已复活！但部分记忆与装备永久消散……`);
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.reviving = false;
                    },

                    async regenAppearance() {
                        if (!this.editChar) return;
                        if (!await this.confirmDialog(`确认花费 ${this.shopCosts?.regenerate_appearance_cost ?? 100} 金币为「${this.editChar.name}」重新生成外貌？`, { confirmText: '重新生成' })) return;
                        this.regenningAppearance = true;
                        try {
                            const r = await this.api('POST', '/api/characters/' + this.editChar.id + '/regenerate-appearance');
                            if (this.user) this.user.coins = r.coins;
                            this.editChar.appearance = r.appearance;
                            this.syncCharacter({ ...this.editChar, appearance: r.appearance });
                            this.showToast('外貌已重新生成');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.regenningAppearance = false;
                    },

                    async regenBackstory() {
                        if (!this.editChar) return;
                        if (!await this.confirmDialog(`确认花费 ${this.shopCosts?.regenerate_backstory_cost ?? 100} 金币为「${this.editChar.name}」重新生成个人经历？`, { confirmText: '重新生成' })) return;
                        this.regenningBackstory = true;
                        try {
                            const r = await this.api('POST', '/api/characters/' + this.editChar.id + '/regenerate-backstory');
                            if (this.user) this.user.coins = r.coins;
                            this.editChar.backstory = r.backstory;
                            this.syncCharacter({ ...this.editChar, backstory: r.backstory });
                            this.showToast('个人经历已重新生成');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.regenningBackstory = false;
                    },

                    async regenTraits() {
                        if (!this.editChar) return;
                        if (!await this.confirmDialog(`确认花费 ${this.shopCosts?.regenerate_traits_cost ?? 100} 金币为「${this.editChar.name}」重新生成性格特征？`, { confirmText: '重新生成' })) return;
                        this.regenningTraits = true;
                        try {
                            const r = await this.api('POST', '/api/characters/' + this.editChar.id + '/regenerate-traits');
                            if (this.user) this.user.coins = r.coins;
                            this.editChar.traits = r.traits;
                            this.syncCharacter({ ...this.editChar, traits: r.traits });
                            this.showToast('性格特征已重新生成');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.regenningTraits = false;
                    },

                    syncCharacter(updated) {
                        if (!updated?.id) return;
                        this.characters = this.characters.map(c => c.id === updated.id ? updated : c);
                        if (this.editChar?.id === updated.id) this.editChar = updated;
                    },

                    async addInventoryItem() {
                        if (!this.editChar?.id) return;
                        const item = (this.inventoryInput || '').trim();
                        if (!item) return;
                        try {
                            const updated = await this.api('POST', '/api/characters/' + this.editChar.id + '/inventory', { item });
                            this.syncCharacter(updated);
                            this.inventoryInput = '';
                            this.showToast('已加入物品栏');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async removeInventoryItem(item) {
                        if (!this.editChar?.id || !item) return;
                        try {
                            const updated = await this.api('DELETE', '/api/characters/' + this.editChar.id + '/inventory/' + encodeURIComponent(item));
                            this.syncCharacter(updated);
                            this.showToast('已从物品栏移除');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async removeSocialRelation(name) {
                        if (!this.editChar?.id || !name) return;
                        if (!await this.confirmDialog('确定要删除与「' + name + '」的社会关系吗？此操作不可撤销。', { danger: true, confirmText: '删除' })) return;
                        try {
                            const updated = await this.api('DELETE', '/api/characters/' + this.editChar.id + '/social-relations/' + encodeURIComponent(name));
                            this.syncCharacter(updated);
                            this.showToast('已删除社会关系');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async removeAsset(name) {
                        if (!this.editChar?.id || !name) return;
                        if (!await this.confirmDialog('确定要删除资产「' + name + '」吗？此操作不可撤销。', { danger: true, confirmText: '删除' })) return;
                        try {
                            const updated = await this.api('DELETE', '/api/characters/' + this.editChar.id + '/assets/' + encodeURIComponent(name));
                            this.syncCharacter(updated);
                            this.showToast('已删除资产');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

};
