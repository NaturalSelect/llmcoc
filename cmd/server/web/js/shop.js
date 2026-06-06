// LLM-COC Shop — Purchases
window.COC = window.COC || {};
window.COC.shop = {
                    async loadShop() {
                        const [items, txns] = await Promise.all([
                            this.api('GET', '/api/shop/items'),
                            this.api('GET', '/api/shop/transactions'),
                        ]);
                        this.shopItems = items || [];
                        this.transactions = txns || [];
                    },
                    // ═══════════════════════════════════════════════════════
                    // Shop
                    // ═══════════════════════════════════════════════════════
                    isInventoryItem(itemType) {
                        return itemType === 'equipment' || itemType === 'weapon' || itemType === 'accessory';
                    },
                    isReviveItem(itemType) {
                        return itemType === 'revive';
                    },
                    requiresCharacter(itemType) {
                        return this.isInventoryItem(itemType) || this.isReviveItem(itemType);
                    },

                    async purchase(item) {
                        if (this.isReviveItem(item.item_type)) {
                            if (!this.shopTargetCharacterId) {
                                this.showToast('请先选择要复活的人物卡', 'error');
                                return;
                            }
                            const target = this.characters.find(c => c.id === Number(this.shopTargetCharacterId));
                            if (!target || target.wound_state !== 'dead') {
                                this.showToast('所选人物卡并非死亡状态，无法使用复活卷轴', 'error');
                                return;
                            }
                            if (!confirm(`确认花费 ${item.price} 金币复活「${target.name}」？\n复活后HP为最大值的一半，随机失去一半装备，遗忘一半法术。`)) return;
                        } else {
                            if (!confirm(`确认花费 ${item.price} 金币购买「${item.name}」？`)) return;
                            if (this.isInventoryItem(item.item_type) && !this.shopTargetCharacterId) {
                                this.showToast('请先选择目标人物卡', 'error');
                                return;
                            }
                        }
                        this.purchasing = true;
                        try {
                            const body = { item_id: item.id };
                            if (this.requiresCharacter(item.item_type)) {
                                body.character_card_id = Number(this.shopTargetCharacterId);
                            }
                            const r = await this.api('POST', '/api/shop/purchase', body);
                            if (this.user) { this.user.coins = r.coins; this.user.card_slots = r.card_slots; }
                            if (r.character_card) this.syncCharacter(r.character_card);
                            this.transactions.unshift({ id: Date.now(), shop_item: item, coins_spent: item.price, created_at: new Date().toISOString() });
                            if (this.isReviveItem(item.item_type)) {
                                this.showToast('复活成功！调查员已苏醒，但部分记忆与装备已消散……');
                            } else {
                                this.showToast('购买成功！');
                            }
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.purchasing = false;
                    },

};
