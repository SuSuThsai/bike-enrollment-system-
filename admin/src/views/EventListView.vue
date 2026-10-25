<script setup lang="ts">
import { computed, reactive, ref, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'

import { errorMessage } from '@/api/http'
import { createEvent, deleteEvent, getEvent, listEvents, updateEvent } from '@/api/events'
import type { EventListItem, RegistrationStatus } from '@/api/types'
import { formatDateTime, formatPrice, toDate } from '@/utils/format'

const router = useRouter()

const loading = ref(false)
const events = ref<EventListItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const status = ref<RegistrationStatus | ''>('')

const dialogVisible = ref(false)
const saving = ref(false)
const deletingId = ref<string | null>(null)
const editingId = ref<string | null>(null)
const form = reactive({
  title: '',
  cover_url: '',
  registration_open_at: null as Date | null,
  registration_deadline: null as Date | null,
  start_time: null as Date | null,
  end_time: null as Date | null,
  location: '',
  description: '',
  rules: '',
})

interface TimeErrors {
  open: string
  deadline: string
  start: string
  end: string
}

// 前端即时校验：registration_open_at < registration_deadline <= start_time < end_time
const timeErrors = computed<TimeErrors>(() => {
  const errors: TimeErrors = { open: '', deadline: '', start: '', end: '' }
  const open = form.registration_open_at
  const deadline = form.registration_deadline
  const start = form.start_time
  const end = form.end_time

  if (!open) errors.open = '请选择报名开放时间'
  if (!deadline) errors.deadline = '请选择报名截止时间'
  if (!start) errors.start = '请选择赛事开始时间'
  if (!end) errors.end = '请选择赛事结束时间'
  if (open && deadline && open >= deadline) {
    errors.deadline = '报名截止时间必须晚于报名开放时间'
  }
  if (deadline && start && deadline > start) {
    errors.deadline = '报名截止时间不能晚于赛事开始时间'
  }
  if (start && end && start >= end) {
    errors.end = '赛事结束时间必须晚于赛事开始时间'
  }
  return errors
})
const hasTimeError = computed(() => Object.values(timeErrors.value).some(Boolean))

const durationText = computed(() => {
  const start = form.start_time
  const end = form.end_time
  if (!start || !end || end <= start) {
    return '—'
  }
  const minutes = Math.floor((end.getTime() - start.getTime()) / 60000)
  return `${Math.floor(minutes / 60)} 小时 ${minutes % 60} 分`
})

function isDeadlineSoon(value?: string | null): boolean {
  if (!value) {
    return false
  }
  const deadline = new Date(value).getTime()
  if (Number.isNaN(deadline)) {
    return false
  }
  const remaining = deadline - Date.now()
  return remaining > 0 && remaining < 24 * 60 * 60 * 1000
}

const statusOptions: Array<{ label: string; value: RegistrationStatus }> = [
  { label: '报名中', value: 'open' },
  { label: '报名未开始', value: 'not_started' },
  { label: '报名结束', value: 'closed' },
]

const statusLabels: Record<string, string> = {
  open: '报名中',
  not_started: '报名未开始',
  closed: '报名结束',
}

function statusLabel(value: string): string {
  return statusLabels[value] || value
}

function statusTagType(value: string): 'success' | 'info' | 'warning' | 'danger' | 'primary' {
  if (value === 'open') return 'success'
  if (value === 'not_started') return 'primary'
  if (value === 'closed') return 'warning'
  return 'info'
}

async function load() {
  loading.value = true
  try {
    const data = await listEvents({
      registration_status: status.value || undefined,
      page: page.value,
      page_size: pageSize.value,
    })
    events.value = data.items
    total.value = data.total
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    loading.value = false
  }
}

function resetForm() {
  editingId.value = null
  form.title = ''
  form.cover_url = ''
  form.registration_open_at = null
  form.registration_deadline = null
  form.start_time = null
  form.end_time = null
  form.location = ''
  form.description = ''
  form.rules = ''
}

function openCreate() {
  resetForm()
  dialogVisible.value = true
}

async function openEdit(row: unknown) {
  const event = row as EventListItem
  try {
    const detail = await getEvent(event.id)
    editingId.value = event.id
    form.title = detail.title
    form.cover_url = detail.cover_url
    form.registration_open_at = toDate(detail.registration_open_at)
    form.registration_deadline = toDate(detail.registration_deadline)
    form.start_time = toDate(detail.start_time)
    form.end_time = toDate(detail.end_time)
    form.location = detail.location
    form.description = detail.description
    form.rules = detail.rules || ''
    dialogVisible.value = true
  } catch (error) {
    ElMessage.error(errorMessage(error))
  }
}

async function save() {
  if (!form.title.trim()) {
    ElMessage.warning('请填写赛事标题')
    return
  }
  if (hasTimeError.value) {
    ElMessage.warning('请先修正时间设置')
    return
  }
  if (!form.location.trim()) {
    ElMessage.warning('请填写地点')
    return
  }
  if (!form.description.trim()) {
    ElMessage.warning('请填写赛事描述')
    return
  }

  saving.value = true
  try {
    // 四个时间字段必填；duration 仅前端展示，不提交。
    const payload = {
      title: form.title.trim(),
      cover_url: form.cover_url.trim(),
      registration_open_at: form.registration_open_at!.toISOString(),
      registration_deadline: form.registration_deadline!.toISOString(),
      start_time: form.start_time!.toISOString(),
      end_time: form.end_time!.toISOString(),
      location: form.location.trim(),
      description: form.description.trim(),
      rules: form.rules.trim(),
    }
    if (editingId.value) {
      await updateEvent(editingId.value, payload)
      ElMessage.success('赛事已更新')
    } else {
      await createEvent(payload)
      ElMessage.success('赛事已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    saving.value = false
  }
}

function changeFilter() {
  page.value = 1
  void load()
}

function changePage(value: number) {
  page.value = value
  void load()
}

function changePageSize(value: number) {
  pageSize.value = value
  page.value = 1
  void load()
}

function goDetail(row: unknown) {
  const event = row as EventListItem
  void router.push(`/events/${event.id}`)
}

async function removeEvent(row: unknown) {
  const event = row as EventListItem
  try {
    await ElMessageBox.confirm(
      `确认删除赛事「${event.title}」？仅当赛事下没有任何组别时才允许删除，删除后不可恢复。`,
      '删除赛事',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  deletingId.value = event.id
  try {
    await deleteEvent(event.id)
    ElMessage.success('赛事已删除')
    await load()
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    deletingId.value = null
  }
}

onMounted(load)
</script>

<template>
  <div class="page">
    <header class="page-header page-header--row">
      <div>
        <h1>赛事管理</h1>
        <p>创建赛事并维护组别、名额与报名配置。</p>
      </div>
      <el-button type="primary" @click="openCreate">新建赛事</el-button>
    </header>

    <el-card shadow="never" class="filter-card">
      <div class="toolbar">
        <el-select
          v-model="status"
          placeholder="赛事状态"
          clearable
          class="status-select"
          @change="changeFilter"
        >
          <el-option
            v-for="item in statusOptions"
            :key="item.value"
            :label="item.label"
            :value="item.value"
          />
        </el-select>
        <el-button @click="changeFilter">刷新</el-button>
      </div>
    </el-card>

    <el-card shadow="never" class="table-card">
      <el-table v-loading="loading" :data="events" row-key="id">
        <el-table-column prop="title" label="赛事" min-width="220">
          <template #default="{ row }">
            <el-link type="primary" :underline="false" @click="goDetail(row)">
              {{ row.title }}
            </el-link>
          </template>
        </el-table-column>
        <el-table-column prop="start_time" label="开始时间" width="180">
          <template #default="{ row }">{{ formatDateTime(row.start_time) }}</template>
        </el-table-column>
        <el-table-column prop="registration_deadline" label="报名截止" width="210">
          <template #default="{ row }">
            <span :class="{ 'deadline-soon': isDeadlineSoon(row.registration_deadline) }">
              {{ row.registration_deadline ? formatDateTime(row.registration_deadline) : '-' }}
            </span>
            <el-tag
              v-if="isDeadlineSoon(row.registration_deadline)"
              type="danger"
              size="small"
              effect="dark"
            >
              即将截止
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="location" label="地点" min-width="140" />
        <el-table-column prop="status" label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.registration_status)" effect="plain">
              {{ statusLabel(row.registration_status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="min_price" label="最低价" width="110">
          <template #default="{ row }">{{ formatPrice(row.min_price) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="240" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="goDetail(row)">组别</el-button>
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button
              link
              type="danger"
              :loading="deletingId === row.id"
              @click="removeEvent(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="暂无赛事" />
        </template>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[10, 20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @current-change="changePage"
          @size-change="changePageSize"
        />
      </div>
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      :title="editingId ? '编辑赛事' : '新建赛事'"
      width="680px"
      destroy-on-close
    >
      <el-form label-width="100px">
        <el-form-item label="标题" required>
          <el-input v-model="form.title" maxlength="200" show-word-limit />
        </el-form-item>
        <el-form-item label="报名开放" required :error="timeErrors.open">
          <el-date-picker
            v-model="form.registration_open_at"
            type="datetime"
            placeholder="选择报名开放时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="报名截止" required :error="timeErrors.deadline">
          <el-date-picker
            v-model="form.registration_deadline"
            type="datetime"
            placeholder="选择报名截止时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="赛事开始" required :error="timeErrors.start">
          <el-date-picker
            v-model="form.start_time"
            type="datetime"
            placeholder="选择赛事开始时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="赛事结束" required :error="timeErrors.end">
          <el-date-picker
            v-model="form.end_time"
            type="datetime"
            placeholder="选择赛事结束时间"
            style="width: 100%"
          />
          <div class="duration-hint">持续时间：{{ durationText }}</div>
        </el-form-item>
        <el-form-item label="地点" required>
          <el-input v-model="form.location" maxlength="255" />
        </el-form-item>
        <el-form-item label="封面 URL">
          <el-input v-model="form.cover_url" placeholder="https://..." />
        </el-form-item>
        <el-form-item label="赛事描述" required>
          <el-input v-model="form.description" type="textarea" :rows="4" />
        </el-form-item>
        <el-form-item label="赛事规则">
          <el-input v-model="form.rules" type="textarea" :rows="3" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" :disabled="hasTimeError" @click="save">
          保存
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.status-select {
  width: 180px;
}

.deadline-soon {
  color: var(--el-color-danger);
  font-weight: 600;
  margin-right: 6px;
}

.duration-hint {
  margin-top: 6px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
