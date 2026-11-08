<script setup lang="ts">
import type { SiteSettings } from '@/api/adminTypesContent'
import PaperCard from '@/components/base/PaperCard.vue'
import SectionHeading from '@/components/base/SectionHeading.vue'
import SettingSwitch from './SettingSwitch.vue'
import type { ToggleKey } from './useSiteSettings'

defineProps<{ settings: SiteSettings }>()
defineEmits<{ toggle: [key: ToggleKey] }>()
</script>

<template>
  <PaperCard>
    <SectionHeading>站点设置</SectionHeading>
    <div class="mt-4 space-y-4">
      <SettingSwitch
        :model-value="settings.require_invite_code"
        label="注册需要邀请码"
        @toggle="$emit('toggle', 'require_invite_code')"
      />
      <SettingSwitch
        :model-value="settings.allow_nsfw"
        label="允许创建 NSFW 房间"
        description="关闭后创建房间时不再显示 NSFW 选项，已创建的房间不受影响"
        @toggle="$emit('toggle', 'allow_nsfw')"
      />
    </div>
  </PaperCard>
</template>
