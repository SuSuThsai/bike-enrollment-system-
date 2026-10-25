<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { errorMessage } from '@/api/http'
import { listEvents } from '@/api/events'
import type { EventListItem, RegistrationStatus } from '@/api/types'
import EventCard from '@/components/EventCard.vue'

const loading = ref(false)
const error = ref('')
const events = ref<EventListItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 9
const status = ref<RegistrationStatus | ''>('')

const filters: Array<{ label: string; value: RegistrationStatus | '' }> = [
  { label: '全部赛事', value: '' },
  { label: '报名中', value: 'open' },
  { label: '报名未开始', value: 'not_started' },
  { label: '报名结束', value: 'closed' },
]

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await listEvents({
      registration_status: status.value || undefined,
      page: page.value,
      page_size: pageSize,
    })
    events.value = data.items
    total.value = data.total
  } catch (err) {
    error.value = errorMessage(err)
    events.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

function selectStatus(value: RegistrationStatus | '') {
  if (status.value === value) {
    return
  }
  status.value = value
  page.value = 1
  void load()
}

function goPage(value: number) {
  if (value < 1 || value > totalPages.value || value === page.value) {
    return
  }
  page.value = value
  void load()
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

onMounted(load)
</script>

<template>
  <div>
    <section class="relative overflow-hidden bg-slate-950 text-white">
      <div class="absolute inset-0 bg-[radial-gradient(circle_at_top_right,rgba(16,185,129,0.32),transparent_45%)]" />
      <div class="absolute -bottom-24 left-1/3 h-72 w-72 rounded-full bg-emerald-500/20 blur-3xl" />
      <div class="container-page relative py-20 sm:py-28">
        <p class="text-xs font-bold uppercase tracking-[0.32em] text-emerald-300">Ride Together</p>
        <h1 class="mt-5 max-w-3xl text-4xl font-black leading-tight tracking-tight sm:text-5xl lg:text-6xl">
          找到下一场<br class="hidden sm:block" />
          值得出发的骑行赛事
        </h1>
        <p class="mt-6 max-w-2xl text-base leading-7 text-slate-300 sm:text-lg">
          浏览赛事与组别、查看报名费用和实时剩余名额。报名与支付请在微信小程序内完成。
        </p>
        <div class="mt-8 flex flex-wrap items-center gap-4 text-sm text-slate-300">
          <span class="rounded-full border border-white/15 bg-white/5 px-4 py-2">实时名额来自报名系统</span>
          <span class="rounded-full border border-white/15 bg-white/5 px-4 py-2">官网不收集报名信息</span>
        </div>
      </div>
    </section>

    <section class="container-page py-12 sm:py-16">
      <div class="flex flex-col gap-5 border-b border-slate-200 pb-8 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p class="text-xs font-bold uppercase tracking-[0.24em] text-emerald-600">Events</p>
          <h2 class="mt-2 text-3xl font-black tracking-tight text-slate-900">赛事列表</h2>
          <p class="mt-2 text-sm text-slate-500">共 {{ total }} 场赛事</p>
        </div>

        <div class="flex flex-wrap gap-2">
          <button
            v-for="item in filters"
            :key="item.value"
            type="button"
            class="rounded-full px-4 py-2 text-sm font-semibold transition"
            :class="status === item.value
              ? 'bg-slate-900 text-white shadow-sm'
              : 'bg-white text-slate-600 ring-1 ring-slate-200 hover:bg-slate-100'"
            @click="selectStatus(item.value)"
          >
            {{ item.label }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
        <div v-for="index in 6" :key="index" class="card overflow-hidden">
          <div class="h-44 animate-pulse bg-slate-200" />
          <div class="space-y-3 p-5">
            <div class="h-5 w-3/4 animate-pulse rounded bg-slate-200" />
            <div class="h-4 w-1/2 animate-pulse rounded bg-slate-100" />
            <div class="h-10 w-full animate-pulse rounded bg-slate-100" />
          </div>
        </div>
      </div>

      <div v-else-if="error" class="card mt-8 p-10 text-center">
        <p class="text-lg font-semibold text-slate-900">赛事加载失败</p>
        <p class="mt-2 text-sm text-slate-500">{{ error }}</p>
        <button type="button" class="btn-secondary mt-6" @click="load">重新加载</button>
      </div>

      <div v-else-if="events.length === 0" class="card mt-8 p-14 text-center">
        <div class="mx-auto flex h-16 w-16 items-center justify-center rounded-2xl bg-slate-100 text-2xl">🚴</div>
        <p class="mt-5 text-lg font-bold text-slate-900">暂无赛事</p>
        <p class="mt-2 text-sm text-slate-500">换个筛选条件看看，或稍后再来。</p>
      </div>

      <div v-else class="mt-8 grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
        <EventCard v-for="event in events" :key="event.id" :event="event" />
      </div>

      <div v-if="totalPages > 1" class="mt-10 flex items-center justify-center gap-3">
        <button
          type="button"
          class="btn-secondary px-4 py-2"
          :disabled="page <= 1"
          :class="{ 'cursor-not-allowed opacity-40': page <= 1 }"
          @click="goPage(page - 1)"
        >
          上一页
        </button>
        <span class="text-sm text-slate-500">第 {{ page }} / {{ totalPages }} 页</span>
        <button
          type="button"
          class="btn-secondary px-4 py-2"
          :disabled="page >= totalPages"
          :class="{ 'cursor-not-allowed opacity-40': page >= totalPages }"
          @click="goPage(page + 1)"
        >
          下一页
        </button>
      </div>
    </section>

    <section class="container-page pb-16">
      <div class="rounded-3xl bg-emerald-50 p-8 ring-1 ring-emerald-100 sm:flex sm:items-center sm:justify-between">
        <div>
          <h2 class="text-xl font-bold text-emerald-950">准备报名？</h2>
          <p class="mt-2 text-sm leading-6 text-emerald-800">
            打开微信扫一扫，进入小程序选择赛事和组别，完成报名与支付。
          </p>
        </div>
        <RouterLink to="/enroll-guide" class="btn-secondary mt-5 border-emerald-200 text-emerald-800 sm:mt-0">
          查看报名指引
        </RouterLink>
      </div>
    </section>
  </div>
</template>
