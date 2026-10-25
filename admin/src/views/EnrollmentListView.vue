<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'

import { errorMessage } from '@/api/http'
import { exportEnrollments, listEnrollments } from '@/api/enrollments'
import { getEvent, listEvents } from '@/api/events'
import type {
  Enrollment,
  EnrollmentStatus,
  EventListItem,
  Group,
} from '@/api/types'
import { formatDateTime, formatFormData } from '@/utils/format'

const loading = ref(false)
const exporting = ref(false)
const events = ref<EventListItem[]>([])
const groups = ref<Group[]>([])
const items = ref<Enrollment[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const eventId = ref('')
const groupId = ref('')
const status = ref<EnrollmentStatus | ''>('')

const statusOptions: Array<{ label: string; value: EnrollmentStatus }> = [
  { label: '待支付', value: 'pending_payment' },
  { label: '已支付', value: 'paid' },
  { label: '已确认', value: 'confirmed' },
  { label: '已取消', value: 'cancelled' },
  { label: '已退款', value: 'refunded' },
]

const statusLabels: Record<string, string> = Object.fromEntries(
  statusOptions.map((item) => [item.value, item.label]),
)

const eventNames = computed(() => {
  const map = new Map<string, string>()
  events.value.forEach((item) => map.set(item.id, item.title))
  return map
})

const groupNames = computed(() => {
  const map = new Map<string, string>()
  groups.value.forEach((item) => map.set(item.id, item.name))
  return map
})

function statusTagType(value: string): 'success' | 'info' | 'warning' | 'danger' {
  if (value === 'paid' || value === 'confirmed') return 'success'
  if (value === 'cancelled' || value === 'refunded') return 'info'
  return 'warning'
}

async function loadEvents() {
  try {
    const data = await listEvents({ page: 1, page_size: 100 })
    events.value = data.items
  } catch (error) {
    ElMessage.error(errorMessage(error))
  }
}

async function loadGroups() {
  if (!eventId.value) {
    groups.value = []
    groupId.value = ''
    return
  }
  try {
    const detail = await getEvent(eventId.value)
    groups.value = detail.groups
    if (groupId.value && !groups.value.some((item) => item.id === groupId.value)) {
      groupId.value = ''
    }
  } catch (error) {
    groups.value = []
    groupId.value = ''
    ElMessage.error(errorMessage(error))
  }
}

async function loadEnrollments() {
  loading.value = true
  try {
    const data = await listEnrollments({
      event_id: eventId.value || undefined,
      group_id: groupId.value || undefined,
      status: status.value || undefined,
      page: page.value,
      page_size: pageSize.value,
    })
    items.value = data.items
    total.value = data.total
    page.value = data.page
    pageSize.value = data.page_size
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    loading.value = false
  }
}

function changeFilter() {
  page.value = 1
  void loadEnrollments()
}

function changePage(value: number) {
  page.value = value
  void loadEnrollments()
}

function changePageSize(value: number) {
  pageSize.value = value
  page.value = 1
  void loadEnrollments()
}

async function handleExport() {
  if (!eventId.value) {
    ElMessage.warning('导出前请先选择赛事')
    return
  }
  exporting.value = true
  try {
    const file = await exportEnrollments({
      event_id: eventId.value,
      group_id: groupId.value || undefined,
    })
    const url = URL.createObjectURL(file.blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = file.filename
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
    URL.revokeObjectURL(url)
    ElMessage.success('导出已开始')
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    exporting.value = false
  }
}

function eventName(id: string): string {
  return eventNames.value.get(id) || id
}

function groupName(id: string): string {
  return groupNames.value.get(id) || id
}

watch(eventId, async () => {
  page.value = 1
  await loadGroups()
  await loadEnrollments()
})

onMounted(async () => {
  await loadEvents()
  await loadEnrollments()
})
</script>

<template>
  <div class="page">
    <header class="page-header page-header--row">
      <div>
        <h1>报名名单</h1>
        <p>按赛事、组别、状态筛选报名记录；导出上限 10000 条。</p>
      </div>
      <el-button type="primary" :loading="exporting" :disabled="!eventId" @click="handleExport">
        导出 Excel
      </el-button>
    </header>

    <el-card shadow="never" class="filter-card">
      <div class="toolbar toolbar--wrap">
        <el-select v-model="eventId" placeholder="赛事" clearable class="filter-select">
          <el-option
            v-for="item in events"
            :key="item.id"
            :label="item.title"
            :value="item.id"
          />
        </el-select>
        <el-select
          v-model="groupId"
          placeholder="组别"
          clearable
          :disabled="!eventId"
          class="filter-select"
        >
          <el-option
            v-for="item in groups"
            :key="item.id"
            :label="item.name"
            :value="item.id"
          />
        </el-select>
        <el-select v-model="status" placeholder="报名状态" clearable class="filter-select">
          <el-option
            v-for="item in statusOptions"
            :key="item.value"
            :label="item.label"
            :value="item.value"
          />
        </el-select>
        <el-button type="primary" plain @click="changeFilter">查询</el-button>
        <el-button @click="loadEnrollments">刷新</el-button>
      </div>
      <div v-if="!eventId" class="filter-hint">选择赛事后可导出 Excel。</div>
    </el-card>

    <el-card shadow="never" class="table-card">
      <el-table v-loading="loading" :data="items" row-key="id">
        <el-table-column prop="id" label="报名 ID" min-width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.id }}</span>
          </template>
        </el-table-column>
        <el-table-column label="赛事 / 组别" min-width="220">
          <template #default="{ row }">
            <div>{{ eventName(row.event_id) }}</div>
            <div class="muted">{{ groupName(row.group_id) }}</div>
          </template>
        </el-table-column>
        <el-table-column prop="user_id" label="用户" min-width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.user_id }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="order_id" label="订单号" min-width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.order_id }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="status" label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)" effect="plain">
              {{ statusLabels[row.status] || row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="form_data" label="表单数据" min-width="220">
          <template #default="{ row }">
            <el-tooltip
              :content="formatFormData(row.form_data)"
              placement="top"
              :show-after="300"
            >
              <span class="ellipsis">{{ formatFormData(row.form_data) }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column prop="created_at" label="报名时间" width="180">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无报名记录" />
        </template>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100, 200]"
          layout="total, sizes, prev, pager, next"
          @current-change="changePage"
          @size-change="changePageSize"
        />
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.filter-select {
  width: 220px;
}

.filter-hint {
  margin-top: 10px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
}

.muted {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.ellipsis {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
