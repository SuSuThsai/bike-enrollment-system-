<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useRoute } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const auth = useAuthStore()

const tokenDialog = ref(false)
const tokenDraft = ref('')

const activeMenu = computed(() => {
  if (route.path.startsWith('/events')) {
    return '/events'
  }
  return route.path
})

watch(tokenDialog, (open) => {
  if (open) {
    tokenDraft.value = auth.token
  }
})

function saveToken() {
  auth.saveToken(tokenDraft.value)
  tokenDialog.value = false
  ElMessage.success(auth.token ? 'Token 已保存' : 'Token 已清除')
}
</script>

<template>
  <el-container class="app-shell">
    <el-header class="app-header">
      <div class="brand">
        <span class="brand-mark">BIKE</span>
        <span>骑行赛事管理后台</span>
      </div>
      <div class="header-actions">
        <el-tag :type="auth.token ? 'success' : 'warning'" effect="plain" round>
          {{ auth.token ? 'Token 已配置' : '未配置 Token' }}
        </el-tag>
        <el-button size="small" @click="tokenDialog = true">设置 Token</el-button>
      </div>
    </el-header>

    <el-container class="app-body">
      <el-aside width="208px" class="app-aside">
        <el-menu :default-active="activeMenu" router class="app-menu">
          <el-menu-item index="/events">赛事管理</el-menu-item>
          <el-menu-item index="/enrollments">报名名单</el-menu-item>
          <el-menu-item index="/stats">统计看板</el-menu-item>
        </el-menu>
      </el-aside>

      <el-main class="app-main">
        <router-view />
      </el-main>
    </el-container>

    <el-dialog v-model="tokenDialog" title="配置管理员 JWT" width="560px">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="后端 admin 接口要求 Authorization: Bearer <JWT>"
        description="请使用 role=admin 的 JWT。Token 仅保存在当前浏览器 localStorage，不会发送到其他地址。"
      />
      <el-input
        v-model="tokenDraft"
        class="token-input"
        type="textarea"
        :rows="6"
        placeholder="粘贴管理员 JWT"
      />
      <template #footer>
        <el-button @click="tokenDialog = false">取消</el-button>
        <el-button type="primary" @click="saveToken">保存</el-button>
      </template>
    </el-dialog>
  </el-container>
</template>

<style scoped>
.app-shell {
  min-height: 100%;
}

.app-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 56px;
  padding: 0 24px;
  background: #fff;
  border-bottom: 1px solid var(--el-border-color-light);
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 16px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.brand-mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 24px;
  border-radius: 6px;
  background: var(--el-color-primary);
  color: #fff;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.08em;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.app-body {
  min-height: calc(100vh - 56px);
}

.app-aside {
  background: #fff;
  border-right: 1px solid var(--el-border-color-light);
}

.app-menu {
  border-right: none;
}

.app-main {
  padding: 24px;
  background: var(--el-bg-color-page);
}

.token-input {
  margin-top: 16px;
}
</style>
