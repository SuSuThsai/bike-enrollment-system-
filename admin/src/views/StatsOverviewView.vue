<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'

import { errorMessage } from '@/api/http'
import { listEvents } from '@/api/events'
import { getStatsOverview } from '@/api/stats'
import type { EventListItem, GroupStats, StatsOverview } from '@/api/types'

const loading = ref(false)
const events = ref<EventListItem[]>([])
const eventId = ref('')
const stats = ref<StatsOverview | null>(null)

const selectedEvent = computed(() => events.value.find((item) => item.id === eventId.value))

function equationHolds(group: GroupStats): boolean {
  return (
    group.remaining_slots +
      group.active_lock_count +
      group.pending_payment_count +
      group.paid_count ===
    group.total_slots
  )
}

function usagePercent(group: GroupStats): number {
  if (group.total_slots <= 0) {
    return 0
  }
  return Math.min(100, Math.max(0, Math.round((group.remaining_slots / group.total_slots) * 100)))
}

async function loadStats() {
  if (!eventId.value) {
    stats.value = null
    return
  }
  loading.value = true
  try {
    stats.value = await getStatsOverview(eventId.value)
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    loading.value = false
  }
}

async function loadEvents() {
  try {
    const data = await listEvents({ page: 1, page_size: 100 })
    events.value = data.items
    if (events.value.length > 0) {
      eventId.value = events.value[0].id
      await loadStats()
    }
  } catch (error) {
    ElMessage.error(errorMessage(error))
  }
}

onMounted(loadEvents)
</script>

<template>
  <div class="page">
    <header class="page-header">
      <h1>统计看板</h1>
      <p>口径与对账等式一致：剩余名额 + 活跃锁 + 待支付 + 已支付/已确认 = 总名额。</p>
    </header>

    <el-card shadow="never" class="filter-card">
      <div class="toolbar">
        <el-select
          v-model="eventId"
          placeholder="选择赛事"
          class="event-select"
          @change="loadStats"
        >
          <el-option
            v-for="item in events"
            :key="item.id"
            :label="item.title"
            :value="item.id"
          />
        </el-select>
        <el-button :loading="loading" @click="loadStats">刷新</el-button>
        <span v-if="selectedEvent" class="muted">
          {{ selectedEvent.location }} · {{ selectedEvent.lifecycle_status }} /
          {{ selectedEvent.registration_status }}
        </span>
      </div>
    </el-card>

    <div v-loading="loading">
      <template v-if="stats && stats.groups.length > 0">
        <div class="stats-grid">
          <el-card
            v-for="group in stats.groups"
            :key="group.group_id"
            shadow="never"
            class="stats-card"
          >
            <template #header>
              <div class="stats-header">
                <div>
                  <div class="group-id mono">{{ group.group_id }}</div>
                  <div class="muted">实时名额口径</div>
                </div>
                <el-tag :type="equationHolds(group) ? 'success' : 'danger'" effect="plain">
                  {{ equationHolds(group) ? '等式成立' : '存在不一致' }}
                </el-tag>
              </div>
            </template>

            <div class="metric-row">
              <div class="metric">
                <span class="metric-label">总名额</span>
                <strong>{{ group.total_slots }}</strong>
              </div>
              <div class="metric">
                <span class="metric-label">Redis 剩余</span>
                <strong>{{ group.remaining_slots }}</strong>
              </div>
            </div>

            <el-progress
              :percentage="usagePercent(group)"
              :stroke-width="8"
              :status="equationHolds(group) ? 'success' : 'exception'"
            />

            <div class="counts">
              <div class="count-item">
                <span>活跃锁</span>
                <strong>{{ group.active_lock_count }}</strong>
              </div>
              <div class="count-item">
                <span>待支付</span>
                <strong>{{ group.pending_payment_count }}</strong>
              </div>
              <div class="count-item">
                <span>已支付/确认</span>
                <strong>{{ group.paid_count }}</strong>
              </div>
            </div>

            <div class="equation mono">
              {{ group.remaining_slots }} + {{ group.active_lock_count }} +
              {{ group.pending_payment_count }} + {{ group.paid_count }} =
              {{ group.total_slots }}
            </div>
          </el-card>
        </div>
      </template>

      <el-empty
        v-else-if="!loading"
        :description="eventId ? '该赛事暂无组别统计' : '请选择赛事'"
      />
    </div>
  </div>
</template>

<style scoped>
.event-select {
  width: 300px;
}

.muted {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 16px;
}

.stats-card {
  border-radius: 10px;
}

.stats-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.group-id {
  font-size: 13px;
  color: var(--el-text-color-primary);
  word-break: break-all;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
}

.metric-row {
  display: flex;
  gap: 32px;
  margin-bottom: 16px;
}

.metric {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.metric strong {
  font-size: 28px;
  line-height: 1.1;
  color: var(--el-text-color-primary);
}

.metric-label {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.counts {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  margin-top: 18px;
}

.count-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 10px;
  border-radius: 8px;
  background: var(--el-fill-color-lighter);
}

.count-item span {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.count-item strong {
  font-size: 18px;
  color: var(--el-text-color-primary);
}

.equation {
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px dashed var(--el-border-color-light);
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
