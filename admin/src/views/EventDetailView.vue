<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'

import { ApiError, errorMessage } from '@/api/http'
import { closeRegistration, getEvent, openRegistration } from '@/api/events'
import { createGroup, deleteGroup, rebuildRemaining, updateGroup } from '@/api/groups'
import { getReleaseHealth } from '@/api/stats'
import type { EventDetail, Group, ReleaseHealth } from '@/api/types'
import { formatDateTime, formatDuration, formatPrice, toDate } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const eventId = computed(() => String(route.params.eventId || ''))

const loading = ref(false)
const event = ref<EventDetail | null>(null)

const deletingGroupId = ref<string | null>(null)
const rebuildingGroupId = ref<string | null>(null)
const registrationLoading = ref(false)

const healthVisible = ref(false)
const healthLoading = ref(false)
const health = ref<ReleaseHealth | null>(null)

const dialogVisible = ref(false)
const saving = ref(false)
const editingGroupId = ref<string | null>(null)
const groupForm = reactive({
  name: '',
  price: 0,
  total_slots: 1,
  start_time: null as Date | null,
  form_schema: '{}',
  disclaimer_version: 'v1',
})

const statusLabels: Record<string, string> = {
  open: '开放',
  closed: '已关闭',
  sold_out: '已售罄',
}

function statusTagType(status: string): 'success' | 'info' | 'warning' | 'danger' {
  if (status === 'open') return 'success'
  if (status === 'sold_out') return 'warning'
  return 'info'
}

async function load() {
  if (!eventId.value) {
    return
  }
  loading.value = true
  try {
    event.value = await getEvent(eventId.value)
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    loading.value = false
  }
}

async function removeGroup(row: unknown) {
  const group = row as Group
  try {
    await ElMessageBox.confirm(
      `确认删除组别「${group.name}」？仅当该组别没有任何锁、报名或订单时才允许删除，删除后会同步清理 Redis 名额计数。`,
      '删除组别',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  deletingGroupId.value = group.id
  try {
    await deleteGroup(group.id)
    ElMessage.success('组别已删除')
    await load()
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    deletingGroupId.value = null
  }
}

async function rebuildGroup(row: unknown, force = false) {
  const group = row as Group
  rebuildingGroupId.value = group.id
  try {
    const result = await rebuildRemaining(group.id, force)
    const modeLabels: Record<string, string> = {
      created: '已补建',
      overwritten: '已覆盖',
      unchanged: '无需变更',
    }
    ElMessage.success(
      `名额${modeLabels[result.mode] || result.mode}：remaining=${result.remaining_slots}（total=${result.total_slots}）`,
    )
    await load()
  } catch (error) {
    if (!force && error instanceof ApiError && error.code === 'GROUP_COUNTER_EXISTS') {
      try {
        await ElMessageBox.confirm(
          `${error.message}。是否强制覆盖当前 Redis 计数？请确认该组别已停止报名流量。`,
          '计数已存在',
          { type: 'warning', confirmButtonText: '强制覆盖', cancelButtonText: '取消' },
        )
        await rebuildGroup(group, true)
      } catch {
        // 用户取消强制覆盖
      }
      return
    }
    ElMessage.error(errorMessage(error))
  } finally {
    rebuildingGroupId.value = null
  }
}

async function openHealth() {
  healthVisible.value = true
  healthLoading.value = true
  try {
    health.value = await getReleaseHealth(eventId.value)
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    healthLoading.value = false
  }
}

async function closeEventRegistration() {
  if (!event.value) {
    return
  }
  let reason = ''
  try {
    const result = await ElMessageBox.prompt(
      '停止报名后新用户将无法锁定名额；已锁定的 lock、待支付订单与已支付报名不受影响。',
      '停止报名',
      {
        confirmButtonText: '停止报名',
        cancelButtonText: '取消',
        inputPlaceholder: '关闭原因（可选，最多 255 字）',
        inputValue: '',
        inputValidator: (value) => !value || value.length <= 255 || '原因最多 255 字',
        type: 'warning',
      },
    )
    reason = result.value || ''
  } catch {
    return
  }

  registrationLoading.value = true
  try {
    const state = await closeRegistration(event.value.id, reason)
    ElMessage.success(`已停止报名（registration_status=${state.registration_status}）`)
    await load()
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    registrationLoading.value = false
  }
}

async function reopenEventRegistration() {
  if (!event.value) {
    return
  }
  try {
    await ElMessageBox.confirm(
      '重新开放报名后，新用户可再次锁定名额；若报名截止时间已过，接口会返回 REGISTRATION_DEADLINE_PASSED。',
      '重新开放报名',
      { type: 'warning', confirmButtonText: '重新开放', cancelButtonText: '取消' },
    )
  } catch {
    return
  }

  registrationLoading.value = true
  try {
    const state = await openRegistration(event.value.id)
    ElMessage.success(`已重新开放报名（registration_status=${state.registration_status}）`)
    await load()
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    registrationLoading.value = false
  }
}

function hasHealthIssue(): boolean {
  return (health.value?.groups || []).some(
    (group) => group.lock_leak_count > 0 || group.pending_timeout_count > 0,
  )
}

function resetGroupForm() {
  editingGroupId.value = null
  groupForm.name = ''
  groupForm.price = 0
  groupForm.total_slots = 1
  groupForm.start_time = null
  groupForm.form_schema = '{}'
  groupForm.disclaimer_version = 'v1'
}

function openCreate() {
  resetGroupForm()
  dialogVisible.value = true
}

function openEdit(row: unknown) {
  const group = row as Group
  editingGroupId.value = group.id
  groupForm.name = group.name
  groupForm.price = group.price
  groupForm.total_slots = group.total_slots
  groupForm.start_time = toDate(group.start_time)
  groupForm.form_schema = JSON.stringify(group.form_schema || {}, null, 2)
  groupForm.disclaimer_version = group.disclaimer_version || 'v1'
  dialogVisible.value = true
}

function parseFormSchema(): Record<string, unknown> {
  const parsed = JSON.parse(groupForm.form_schema || '{}') as unknown
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('form_schema 必须是 JSON 对象')
  }
  return parsed as Record<string, unknown>
}

async function saveGroup() {
  if (!groupForm.name.trim()) {
    ElMessage.warning('请填写组别名称')
    return
  }
  if (groupForm.price < 0) {
    ElMessage.warning('价格不能为负')
    return
  }
  if (groupForm.total_slots < 1) {
    ElMessage.warning('总名额必须大于 0')
    return
  }
  if (!groupForm.start_time) {
    ElMessage.warning('请选择开始时间')
    return
  }
  if (!groupForm.disclaimer_version.trim()) {
    ElMessage.warning('请填写免责协议版本')
    return
  }

  let formSchema: Record<string, unknown>
  try {
    formSchema = parseFormSchema()
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : 'form_schema 格式错误')
    return
  }

  saving.value = true
  try {
    const payload = {
      name: groupForm.name.trim(),
      price: groupForm.price,
      total_slots: groupForm.total_slots,
      start_time: groupForm.start_time.toISOString(),
      form_schema: formSchema,
      disclaimer_version: groupForm.disclaimer_version.trim(),
    }
    if (editingGroupId.value) {
      const result = await updateGroup(editingGroupId.value, payload)
      ElMessage.success(`组别已更新，当前剩余 ${result.remaining_slots} 个名额`)
    } else {
      await createGroup({ ...payload, event_id: eventId.value })
      ElMessage.success('组别已创建并完成 Redis 名额初始化')
    }
    dialogVisible.value = false
    await load()
  } catch (error) {
    ElMessage.error(errorMessage(error))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page" v-loading="loading">
    <el-page-header class="page-back" @back="router.push('/events')">
      <template #content>
        <span class="page-back-title">赛事详情</span>
      </template>
    </el-page-header>

    <template v-if="event">
      <header class="page-header page-header--row">
        <div>
          <h1>{{ event.title }}</h1>
          <p>{{ event.location }} · {{ formatDateTime(event.start_time) }}</p>
        </div>
        <div class="header-actions">
          <el-button @click="openHealth">释放健康检查</el-button>
          <el-button
            v-if="event.registration_status !== 'closed'"
            type="danger"
            plain
            :loading="registrationLoading"
            @click="closeEventRegistration"
          >
            停止报名
          </el-button>
          <el-button
            v-else
            type="success"
            plain
            :loading="registrationLoading"
            @click="reopenEventRegistration"
          >
            重新开放报名
          </el-button>
          <el-button type="primary" @click="openCreate">新增组别</el-button>
        </div>
      </header>

      <el-card shadow="never" class="filter-card">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="生命周期">{{ event.lifecycle_status }}</el-descriptions-item>
          <el-descriptions-item label="报名状态">
            <el-tag
              :type="event.registration_status === 'open'
                ? 'success'
                : event.registration_status === 'not_started'
                  ? 'primary'
                  : 'warning'"
              effect="plain"
            >
              {{ event.registration_status }}
            </el-tag>
          </el-descriptions-item>
          <el-descriptions-item
            v-if="event.registration_closed_reason"
            label="关闭原因"
            :span="2"
          >
            {{ event.registration_closed_reason }}
          </el-descriptions-item>
          <el-descriptions-item label="报名开放">
            {{ event.registration_open_at ? formatDateTime(event.registration_open_at) : '不限' }}
          </el-descriptions-item>
          <el-descriptions-item label="报名截止">
            {{ event.registration_deadline ? formatDateTime(event.registration_deadline) : '不限' }}
          </el-descriptions-item>
          <el-descriptions-item label="赛事开始">{{ formatDateTime(event.start_time) }}</el-descriptions-item>
          <el-descriptions-item label="赛事结束">{{ formatDateTime(event.end_time) }}</el-descriptions-item>
          <el-descriptions-item label="持续时间" :span="2">
            {{ formatDuration(event.duration_minutes) }}
          </el-descriptions-item>
          <el-descriptions-item label="最低价">{{ formatPrice(event.min_price) }}</el-descriptions-item>
          <el-descriptions-item label="赛事描述" :span="2">
            {{ event.description }}
          </el-descriptions-item>
          <el-descriptions-item label="赛事规则" :span="2">
            {{ event.rules || '-' }}
          </el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-alert
        class="group-tip"
        type="info"
        :closable="false"
        show-icon
        title="名额调整由后端原子同步 Redis"
        description="修改 total_slots 时，后端会按差值调整剩余名额；新剩余为负时拒绝操作。"
      />

      <el-card shadow="never" class="table-card">
        <template #header>
          <div class="card-header">
            <span>组别管理</span>
            <span class="muted">共 {{ event.groups.length }} 个组别</span>
          </div>
        </template>
        <el-table :data="event.groups" row-key="id">
          <el-table-column prop="name" label="组别" min-width="150" />
          <el-table-column prop="price" label="价格" width="110">
            <template #default="{ row }">{{ formatPrice(row.price) }}</template>
          </el-table-column>
          <el-table-column prop="total_slots" label="总名额" width="100" />
          <el-table-column prop="remaining_slots" label="剩余名额" width="110">
            <template #default="{ row }">
              <el-tag :type="row.remaining_slots > 0 ? 'success' : 'warning'" effect="plain">
                {{ row.remaining_slots }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="start_time" label="开始时间" width="180">
            <template #default="{ row }">{{ formatDateTime(row.start_time) }}</template>
          </el-table-column>
          <el-table-column prop="status" label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="statusTagType(row.status)" effect="plain">
                {{ statusLabels[row.status] || row.status }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="表单字段" min-width="180">
            <template #default="{ row }">
              <el-tooltip
                :content="JSON.stringify(row.form_schema || {})"
                placement="top"
                :show-after="300"
              >
                <span class="schema-preview">{{ JSON.stringify(row.form_schema || {}) }}</span>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="230" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
              <el-button
                link
                type="primary"
                :loading="rebuildingGroupId === row.id"
                @click="rebuildGroup(row)"
              >
                重建名额
              </el-button>
              <el-button
                link
                type="danger"
                :loading="deletingGroupId === row.id"
                @click="removeGroup(row)"
              >
                删除
              </el-button>
            </template>
          </el-table-column>
          <template #empty>
            <el-empty description="该赛事还没有组别" />
          </template>
        </el-table>
      </el-card>
    </template>

    <el-empty v-else-if="!loading" description="赛事不存在或加载失败" />

    <el-dialog
      v-model="dialogVisible"
      :title="editingGroupId ? '编辑组别' : '新增组别'"
      width="720px"
      destroy-on-close
    >
      <el-form label-width="120px">
        <el-form-item label="组别名称" required>
          <el-input v-model="groupForm.name" maxlength="64" show-word-limit />
        </el-form-item>
        <el-form-item label="价格（分）" required>
          <el-input-number v-model="groupForm.price" :min="0" :step="100" />
        </el-form-item>
        <el-form-item label="总名额" required>
          <el-input-number v-model="groupForm.total_slots" :min="1" :step="1" />
        </el-form-item>
        <el-form-item label="开始时间" required>
          <el-date-picker
            v-model="groupForm.start_time"
            type="datetime"
            placeholder="选择开始时间"
            style="width: 100%"
          />
        </el-form-item>
        <el-form-item label="form_schema" required>
          <el-input
            v-model="groupForm.form_schema"
            type="textarea"
            :rows="8"
            placeholder='例如 {"fields":[{"name":"name","label":"姓名","type":"text"}]}'
          />
          <div class="form-hint">必须是 JSON 对象，后端会按此渲染报名动态表单。</div>
        </el-form-item>
        <el-form-item label="免责协议版本" required>
          <el-input v-model="groupForm.disclaimer_version" maxlength="64" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveGroup">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog
      v-model="healthVisible"
      title="释放健康检查（只读）"
      width="900px"
      destroy-on-close
    >
      <div v-loading="healthLoading">
        <el-alert
          v-if="health"
          :type="hasHealthIssue() ? 'error' : 'success'"
          :closable="false"
          show-icon
          :title="hasHealthIssue() ? '发现释放异常，请按样例排查' : '未发现锁泄漏与支付超时遗漏'"
          :description="`判定阈值：锁过期超过 ${health.thresholds.lock_overdue_seconds} 秒仍为 active；订单 pending_payment 超过 ${health.thresholds.pay_timeout_minutes} 分钟未释放。检查时间：${formatDateTime(health.checked_at)}`"
        />
        <el-table
          v-if="health"
          :data="health.groups"
          row-key="group_id"
          class="health-table"
        >
          <el-table-column type="expand">
            <template #default="{ row }">
              <div class="health-samples">
                <div v-if="row.leak_samples.length">
                  <strong>锁泄漏样例：</strong>
                  <div v-for="sample in row.leak_samples" :key="sample.token" class="sample-line">
                    <code>{{ sample.token }}</code> · expires_at
                    {{ formatDateTime(sample.expires_at) }} · 已过期 {{ sample.overdue_seconds }}s
                  </div>
                </div>
                <div v-if="row.pending_samples.length">
                  <strong>支付超时样例：</strong>
                  <div
                    v-for="sample in row.pending_samples"
                    :key="sample.order_id"
                    class="sample-line"
                  >
                    <code>{{ sample.order_id }}</code> · created_at
                    {{ formatDateTime(sample.created_at) }} · 已超时 {{ sample.overdue_minutes }}min
                  </div>
                </div>
                <div
                  v-if="!row.leak_samples.length && !row.pending_samples.length"
                  class="muted"
                >
                  无异常样例
                </div>
              </div>
            </template>
          </el-table-column>
          <el-table-column prop="group_name" label="组别" min-width="160" />
          <el-table-column label="锁泄漏" width="110">
            <template #default="{ row }">
              <el-tag :type="row.lock_leak_count > 0 ? 'danger' : 'success'" effect="plain">
                {{ row.lock_leak_count }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="最大超期" width="120">
            <template #default="{ row }">{{ row.max_overdue_seconds }} s</template>
          </el-table-column>
          <el-table-column label="支付超时遗漏" width="140">
            <template #default="{ row }">
              <el-tag
                :type="row.pending_timeout_count > 0 ? 'danger' : 'success'"
                effect="plain"
              >
                {{ row.pending_timeout_count }}
              </el-tag>
            </template>
          </el-table-column>
        </el-table>
        <el-empty v-else-if="!healthLoading" description="暂无检查结果" />
      </div>
      <template #footer>
        <el-button @click="healthVisible = false">关闭</el-button>
        <el-button type="primary" :loading="healthLoading" @click="openHealth">
          重新检查
        </el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-back {
  margin-bottom: 16px;
}

.page-back-title {
  font-weight: 600;
}

.group-tip {
  margin-bottom: 16px;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.muted {
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.header-actions {
  display: flex;
  gap: 12px;
}

.health-table {
  margin-top: 12px;
}

.health-samples {
  padding: 4px 16px;
}

.sample-line {
  margin-top: 4px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.schema-preview {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--el-text-color-secondary);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
}

.form-hint {
  margin-top: 6px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>
