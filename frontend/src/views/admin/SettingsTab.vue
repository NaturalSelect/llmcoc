<script setup lang="ts">
import { onMounted } from 'vue'
import BalanceRulesCard from '@/components/admin/settings/BalanceRulesCard.vue'
import CostSettingsCard from '@/components/admin/settings/CostSettingsCard.vue'
import InviteCodesCard from '@/components/admin/settings/InviteCodesCard.vue'
import LawyerSimilarityCard from '@/components/admin/settings/LawyerSimilarityCard.vue'
import SessionMaintenanceCard from '@/components/admin/settings/SessionMaintenanceCard.vue'
import SiteToggleCard from '@/components/admin/settings/SiteToggleCard.vue'
import { useSiteSettings } from '@/components/admin/settings/useSiteSettings'
import { useToastRun } from '@/components/admin/scenarios/useToastRun'

const run = useToastRun()
const { settings, load, toggle, setCost, saveCost, setSimilarity, saveSimilarity, saveBalanceRules, endAllSessions } = useSiteSettings()

onMounted(() => run(load))
</script>

<template>
  <div class="space-y-5">
    <SiteToggleCard :settings="settings" @toggle="toggle" />
    <SessionMaintenanceCard @end-all="endAllSessions" />
    <CostSettingsCard :settings="settings" @set="setCost" @save="saveCost" />
    <LawyerSimilarityCard :settings="settings" @set="setSimilarity" @save="saveSimilarity" />
    <BalanceRulesCard v-model="settings.balance_rules" @save="saveBalanceRules" />
    <InviteCodesCard />
  </div>
</template>
