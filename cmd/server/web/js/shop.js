// LLM-COC Shop — Purchases
window.COC = window.COC || {};
window.COC.shop = {
                    async loadShop() {
                        this.shopLoading = true;
                        try {
                            await Promise.all([this.loadShopItems(1), this.loadTransactions(1)]);
                        } finally { this.shopLoading = false; }
                    },
                    async loadShopItems(page) {
                        const nextPage = Math.max(1, Number(page || this.shopItemPage || 1));
                        const pageSize = Math.max(1, Number(this.shopItemPageSize || 20));
                        const resp = await this.api('GET', `/api/shop/items?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);
                        this.shopItems = resp?.items || [];
                        this.shopItemPage = Math.max(1, Number(resp?.page || nextPage));
                        this.shopItemPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                        this.shopItemTotal = Math.max(0, Number(resp?.total || 0));
                        this.shopItemTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                    },
                    async prevShopItemPage() {
                        if (this.shopItemPage <= 1) return;
                        await this.loadShopItems(this.shopItemPage - 1);
                    },
                    async nextShopItemPage() {
                        if (this.shopItemPage >= this.shopItemTotalPages) return;
                        await this.loadShopItems(this.shopItemPage + 1);
                    },
                    async loadTransactions(page) {
                        const nextPage = Math.max(1, Number(page || this.transactionPage || 1));
                        const pageSize = Math.max(1, Number(this.transactionPageSize || 20));
                        const resp = await this.api('GET', `/api/shop/transactions?page=${encodeURIComponent(nextPage)}&page_size=${encodeURIComponent(pageSize)}`);
                        this.transactions = resp?.items || [];
                        this.transactionPage = Math.max(1, Number(resp?.page || nextPage));
                        this.transactionPageSize = Math.max(1, Number(resp?.page_size || pageSize));
                        this.transactionTotal = Math.max(0, Number(resp?.total || 0));
                        this.transactionTotalPages = Math.max(1, Number(resp?.total_pages || 1));
                    },
                    async prevTransactionPage() {
                        if (this.transactionPage <= 1) return;
                        await this.loadTransactions(this.transactionPage - 1);
                    },
                    async nextTransactionPage() {
                        if (this.transactionPage >= this.transactionTotalPages) return;
                        await this.loadTransactions(this.transactionPage + 1);
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
                            if (!await this.confirmDialog(`确认花费 ${item.price} 金币复活「${target.name}」？\n复活后HP为最大值的一半，随机失去一半装备，遗忘一半法术。`, { confirmText: '复活' })) return;
                        } else {
                            if (!await this.confirmDialog(`确认花费 ${item.price} 金币购买「${item.name}」？`, { confirmText: '购买' })) return;
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
                            await this.loadTransactions(1);
                            if (this.isReviveItem(item.item_type)) {
                                this.showToast('复活成功！调查员已苏醒，但部分记忆与装备已消散……');
                            } else {
                                this.showToast('购买成功！');
                            }
                        } catch (e) { this.showToast(e.message, 'error'); }
                        this.purchasing = false;
                    },

};
