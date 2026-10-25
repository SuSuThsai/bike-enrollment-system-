<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { getEvent } from '@/api/events'
import { errorMessage } from '@/api/http'
import type { EventDetail, Group } from '@/api/types'
import {
  canRegister,
  formatDateTime,
  formatDuration,
  formatPrice,
  isDeadlineSoon,
  statusClass,
  statusLabel,
} from '@/utils/format'

const route = useRoute()
const router = useRouter()

const event = ref<EventDetail | null>(null)
const loading = ref(false)
const error = ref('')
const imageFailed = ref(false)

const eventId = computed(() => String(route.params.eventId || ''))
const totalRemaining = computed(() =>
  event.value?.groups.reduce((sum, group) => sum + group.remaining_slots, 0) ?? 0,
)

async function load() {
  if (!eventId.value) {
    return
  }
  loading.value = true
  error.value = ''
  imageFailed.value = false
  try {
    event.value = await getEvent(eventId.value)
  } catch (err) {
    error.value = errorMessage(err)
    event.value = null
  } finally {
    loading.value = false
  }
}

function enroll(group: Group) {
  if (!event.value) {
    return
  }
  void router.push({
    path: '/enroll-guide',
    query: {
      event_id: event.value.id,
      group_id: group.id,
    },
  })
}

function groupStatusText(group: Group): string {
  if (group.remaining_slots <= 0) {
    return '已满员'
  }
  if (group.status === 'closed') {
    return '已关闭'
  }
  if (group.status === 'sold_out') {
    return '已售罄'
  }
  return '可报名'
}

watch(eventId, load, { immediate: true })
</script>

<template>
  <div>
    <div v-if="loading" class="container-page py-16">
      <div class="h-72 animate-pulse rounded-3xl bg-slate-200" />
      <div class="mt-8 grid gap-6 lg:grid-cols-3">
        <div class="h-48 animate-pulse rounded-3xl bg-slate-100 lg:col-span-2" />
        <div class="h-48 animate-pulse rounded-3xl bg-slate-100" />
      </div>
    </div>

    <div v-else-if="error || !event" class="container-page py-24">
      <div class="card mx-auto max-w-xl p-10 text-center">
        <p class="text-sm font-bold uppercase tracking-[0.24em] text-rose-500">Not Found</p>
        <h1 class="mt-3 text-2xl font-black text-slate-900">赛事不存在或已下线</h1>
        <p class="mt-3 text-sm text-slate-500">{{ error || '请返回赛事列表重新选择。' }}</p>
        <div class="mt-7 flex justify-center gap-3">
          <RouterLink to="/" class="btn-primary">返回赛事列表</RouterLink>
          <button type="button" class="btn-secondary" @click="load">重新加载</button>
        </div>
      </div>
    </div>

    <template v-else>
      <section class="bg-slate-950 text-white">
        <div class="container-page py-10 sm:py-14">
          <RouterLink to="/" class="text-sm text-slate-400 transition hover:text-white">
            ← 返回赛事列表
          </RouterLink>

          <div class="mt-8 grid gap-8 lg:grid-cols-[1.35fr_0.65fr] lg:items-end">
            <div>
              <div class="flex flex-wrap items-center gap-3">
                <span
                  class="rounded-full px-3 py-1 text-xs font-semibold ring-1"
                  :class="statusClass(event.registration_status, event.lifecycle_status)"
                >
                  {{ statusLabel(event.registration_status, event.lifecycle_status) }}
                </span>
                <span class="text-xs uppercase tracking-[0.28em] text-emerald-300">Bike Event</span>
              </div>
              <p
                v-if="event.registration_status === 'closed' && event.registration_closed_reason"
                class="mt-3 text-sm text-amber-300"
              >
                报名关闭原因：{{ event.registration_closed_reason }}
              </p>
              <p
                v-else-if="event.registration_status === 'not_started' && event.registration_open_at"
                class="mt-3 text-sm text-sky-300"
              >
                报名将于 {{ formatDateTime(event.registration_open_at) }} 开放
              </p>
              <p v-else-if="event.registration_status === 'closed'" class="mt-3 text-sm text-amber-300">
                报名已截止
              </p>
              <h1 class="mt-5 text-4xl font-black leading-tight tracking-tight sm:text-5xl">
                {{ event.title }}
              </h1>
              <div class="mt-6 flex flex-wrap gap-x-8 gap-y-3 text-sm text-slate-300">
                <span class="flex items-center gap-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                  报名：{{ formatDateTime(event.registration_open_at) }} 至
                  {{ formatDateTime(event.registration_deadline) }}
                  <span
                    v-if="isDeadlineSoon(event.registration_deadline)"
                    class="rounded-full bg-rose-500/20 px-2 py-0.5 text-xs font-semibold text-rose-200"
                  >
                    即将截止
                  </span>
                </span>
                <span class="flex items-center gap-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                  赛事：{{ formatDateTime(event.start_time) }} 至 {{ formatDateTime(event.end_time) }}
                </span>
                <span class="flex items-center gap-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                  持续时间：{{ formatDuration(event.duration_minutes) }}
                </span>
                <span class="flex items-center gap-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                  {{ event.location }}
                </span>
                <span class="flex items-center gap-2">
                  <span class="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                  剩余 {{ totalRemaining }} 个名额
                </span>
              </div>
            </div>

            <div class="rounded-3xl border border-white/10 bg-white/5 p-6 backdrop-blur">
              <p class="text-xs uppercase tracking-[0.24em] text-slate-400">最低报名费</p>
              <p class="mt-2 text-4xl font-black text-white">{{ formatPrice(event.min_price) }}</p>
              <p class="mt-3 text-sm leading-6 text-slate-400">
                报名与支付在小程序内完成，官网不收集报名资料。
              </p>
              <a href="#groups" class="btn-primary mt-6 w-full bg-emerald-500 hover:bg-emerald-400">
                查看组别并报名
              </a>
            </div>
          </div>
        </div>
      </section>

      <section class="container-page grid gap-8 py-12 lg:grid-cols-[1.5fr_0.5fr]">
        <div class="space-y-8">
          <article class="card p-7 sm:p-9">
            <p class="text-xs font-bold uppercase tracking-[0.24em] text-emerald-600">About</p>
            <h2 class="mt-2 text-2xl font-black text-slate-900">赛事介绍</h2>
            <div class="mt-5 whitespace-pre-line text-base leading-8 text-slate-600">
              {{ event.description }}
            </div>
          </article>

          <article v-if="event.rules" class="card p-7 sm:p-9">
            <p class="text-xs font-bold uppercase tracking-[0.24em] text-emerald-600">Rules</p>
            <h2 class="mt-2 text-2xl font-black text-slate-900">赛事规则</h2>
            <div class="mt-5 whitespace-pre-line text-base leading-8 text-slate-600">
              {{ event.rules }}
            </div>
          </article>

          <section id="groups" class="scroll-mt-24">
            <div class="flex items-end justify-between">
              <div>
                <p class="text-xs font-bold uppercase tracking-[0.24em] text-emerald-600">Groups</p>
                <h2 class="mt-2 text-2xl font-black text-slate-900">组别与名额</h2>
              </div>
              <span class="text-sm text-slate-500">{{ event.groups.length }} 个组别</span>
            </div>

            <div v-if="event.groups.length === 0" class="card mt-5 p-10 text-center text-sm text-slate-500">
              暂未开放组别，请稍后再来。
            </div>

            <div v-else class="mt-5 space-y-4">
              <article
                v-for="group in event.groups"
                :key="group.id"
                class="card flex flex-col gap-5 p-6 sm:flex-row sm:items-center sm:justify-between"
              >
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-3">
                    <h3 class="text-xl font-bold text-slate-900">{{ group.name }}</h3>
                    <span
                      class="rounded-full px-2.5 py-1 text-xs font-semibold"
                      :class="groupStatusText(group) === '可报名'
                        ? 'bg-emerald-50 text-emerald-700'
                        : 'bg-slate-100 text-slate-500'"
                    >
                      {{ groupStatusText(group) }}
                    </span>
                  </div>
                  <p class="mt-2 text-sm text-slate-500">
                    开始时间：{{ formatDateTime(group.start_time) }}
                  </p>
                  <p class="mt-1 text-sm text-slate-500">
                    名额：{{ group.remaining_slots }} / {{ group.total_slots }}
                  </p>
                </div>

                <div class="flex items-center justify-between gap-6 sm:flex-col sm:items-end">
                  <p class="text-2xl font-black text-emerald-700">{{ formatPrice(group.price) }}</p>
                  <button
                    type="button"
                    class="btn-primary min-w-32"
                    :disabled="!canRegister(event.registration_status, group.status, group.remaining_slots)"
                    @click="enroll(group)"
                  >
                    {{ canRegister(event.registration_status, group.status, group.remaining_slots) ? '立即报名' : '暂不可报名' }}
                  </button>
                </div>
              </article>
            </div>
          </section>
        </div>

        <aside class="space-y-6">
          <div class="card overflow-hidden">
            <div v-if="event.cover_url && !imageFailed" class="aspect-[4/3] bg-slate-100">
              <img
                :src="event.cover_url"
                :alt="event.title"
                class="h-full w-full object-cover"
                @error="imageFailed = true"
              />
            </div>
            <div v-else class="flex aspect-[4/3] items-center justify-center bg-gradient-to-br from-slate-900 to-emerald-900 p-8 text-center">
              <span class="text-2xl font-black text-white">{{ event.title }}</span>
            </div>
            <div class="p-6">
              <h2 class="text-lg font-bold text-slate-900">报名方式</h2>
              <ol class="mt-4 space-y-3 text-sm leading-6 text-slate-600">
                <li>1. 点击「立即报名」进入小程序码指引页。</li>
                <li>2. 使用微信扫一扫，打开报名小程序。</li>
                <li>3. 选择赛事与组别，填写资料并支付。</li>
              </ol>
              <RouterLink to="/enroll-guide" class="btn-secondary mt-5 w-full">查看报名指引</RouterLink>
            </div>
          </div>

          <div class="rounded-3xl bg-amber-50 p-6 text-sm leading-6 text-amber-900 ring-1 ring-amber-100">
            官网仅用于展示信息，报名、支付、退款等操作均以小程序内页面为准。
          </div>
        </aside>
      </section>
    </template>
  </div>
</template>
