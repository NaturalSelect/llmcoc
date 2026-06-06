// LLM-COC Dashboard — Character management
window.COC = window.COC || {};
window.COC.dashboard = {
                    async loadCharacters() { this.characters = (await this.api('GET', '/api/characters')) || []; },
                    async loadDeadCharacters() { this.deadCharacters = (await this.api('GET', '/api/characters/dead')) || []; },
                    // ══════════════════════════════════════════════════════════════════════
                    // Characters
                    // ══════════════════════════════════════════════════════════════════════
                    openCreateChar() { this.charForm = { name: '', race: '人类', age: 25, gender: '', occupation: '', birthplace: '', backstory: '', traits: '' }; this.modal = 'createChar'; },
                    openGenerateChar() { this.genForm = { name: '', race: '人类', gender: '', age: '', occupation: '', era: '', background: '' }; this.modal = 'generateChar'; },
                    openCharDetail(c) { this.editChar = c; this.inventoryInput = ''; this.modal = 'charDetail'; },
                    openSessionPlayerCharDetail(player) {
                        if (!player?.character_card) {
                            this.showToast('该玩家未绑定人物卡', 'error');
                            return;
                        }
                        if (player.user_id !== this.user?.id) {
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

                    async createChar() {
                        if (!this.charForm.name.trim()) return;
                        this.loading = true;
                        try {
                            const c = await this.api('POST', '/api/characters', this.charForm);
                            this.characters.push(c); this.modal = null;
                            await this.loadMe(); this.showToast('人物卡创建成功！');
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
                        if (!confirm('确认删除此人物卡？')) return;
                        try {
                            await this.api('DELETE', '/api/characters/' + id);
                            this.characters = this.characters.filter(c => c.id !== id);
                            await this.loadDeadCharacters();
                            this.showToast('人物卡已删除');
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async deleteDeadCharacter(char) {
                        if (!confirm(`确认彻底删除「${char.name}」？此操作不可撤销，角色将永久消逝。`)) return;
                        try {
                            await this.api('DELETE', '/api/characters/' + char.id + '/dead');
                            this.deadCharacters = this.deadCharacters.filter(c => c.id !== char.id);
                            this.showToast(`${char.name} 已彻底消逝……`);
                        } catch (e) { this.showToast(e.message, 'error'); }
                    },

                    async reviveCharacter(char) {
                        if (!confirm(`确认花费 2000 金币复活「${char.name}」？\n复活后HP为最大值的一半，随机失去一半装备，遗忘一半法术。`)) return;
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
                        if (!confirm(`确认花费 100 金币为「${this.editChar.name}」重新生成外貌？`)) return;
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
                        if (!confirm(`确认花费 100 金币为「${this.editChar.name}」重新生成背景故事？`)) return;
                        this.regenningBackstory = true;
                        try {
                            const r = await this.api('POST', '/api/characters/' + this.editChar.id + '/regenerate-backstory');
                            if (this.user) this.user.coins = r.coins;
                            this.editChar.backstory = r.backstory;
                            this.syncCharacter({ ...this.editChar, backstory: r.backstory });
                            this.showToast('背景故事已重新生成');
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.regenningBackstory = false;
                    },

                    async regenTraits() {
                        if (!this.editChar) return;
                        if (!confirm(`确认花费 100 金币为「${this.editChar.name}」重新生成性格特征？`)) return;
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

};
