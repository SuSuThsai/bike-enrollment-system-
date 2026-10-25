<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { getEvent } from '@/api/events'
import { errorMessage } from '@/api/http'
import type { EventDetail, Group } from '@/api/types'
import { formatDateTime, formatPrice } from '@/utils/format'

const route = useRoute()
const eventId = computed(() => String(route.query.event_id || ''))
const groupId = computed(() => String(route.query.group_id || ''))

const event = ref<EventDetail | null>(null)
const loading = ref(false)
const loadError = ref('')

const codeUrl =
  (import.meta.env.VITE_MINIPROGRAM_CODE_URL as string | undefined) || '/miniapp-code.svg'
const isPlaceholderCode = !import.meta.env.VITE_MINIPROGRAM_CODE_URL
const selectedGroup = computed<Group | null>(() => {
  if (!event.value || !groupId.value) {
    return null
  }
  return event.value.groups.find((group) => group.id === groupId.value) || null
})

async function load() {
  if (!eventId.value) {
    return
  }
  loading.value = true
  loadError.value = ''
  try {
    event.value = await getEvent(eventId.value)
  } catch (error) {
    loadError.value = errorMessage(error)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <section class="bg-slate-950 text-white">
    <div class="container-page py-12 sm:py-16">
      <RouterLink to="/" class="text-sm text-slate-400 transition hover:text-white">
        ← 返回赛事列表
      </RouterLink>
      <div class="mt-6 grid gap-10 lg:grid-cols-[1fr_380px] lg:items-center">
        <div>
          <p class="text-xs font-bold uppercase tracking-[0.3em] text-emerald-300">Enroll Guide</p>
          <h1 class="mt-4 text-4xl font-black tracking-tight sm:text-5xl">扫码进入小程序报名</h1>
          <p class="mt-5 max-w-xl text-base leading-8 text-slate-300">
            官网不直接提交报名信息。请使用微信扫一扫打开小程序，选择赛事和组别后完成报名与支付。
          </p>

          <div v-if="event" class="mt-8 rounded-2xl border border-white/10 bg-white/5 p-5">
            <p class="text-sm font-semibold text-white">{{ event.title }}</p>
            <p v-if="selectedGroup" class="mt-2 text-sm text-slate-300">
              已选组别：{{ selectedGroup.name }} · {{ formatPrice(selectedGroup.price) }} ·
              剩余 {{ selectedGroup.remaining_slots }} 个名额
            </p>
            <p v-else class="mt-2 text-sm text-slate-400">进入小程序后可选择具体组别。</p>
          </div>
          <p v-else-if="loadError" class="mt-6 text-sm text-amber-300">{{ loadError }}</p>
        </div>

        <div class="mx-auto w-full max-w-sm rounded-3xl bg-white p-6 text-center text-slate-900 shadow-2xl">
          <p class="text-xs font-bold uppercase tracking-[0.24em] text-emerald-600">WeChat Mini Program</p>
          <h2 class="mt-2 text-xl font-black">微信扫码报名</h2>
          <div class="mt-5 rounded-2xl border border-slate-200 bg-slate-50 p-4">
            <img :src="codeUrl" alt="报名小程序码" class="mx-auto h-56 w-56 object-contain" />
          </div>
          <p class="mt-4 text-sm text-slate-500">打开微信「扫一扫」，扫描上方小程序码</p>
          <p v-if="isPlaceholderCode" class="mt-3 rounded-xl bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-700">
            当前为演示占位码。部署时请配置 VITE_MINIPROGRAM_CODE_URL 指向真实小程序码。
          </p>
          <p v-else class="mt-3 text-xs text-slate-400">小程序码由运营配置</p>
        </div>
      </div>
    </div>
  </section>

  <section class="container-page py-12 sm:py-16">
    <div class="grid gap-6 md:grid-cols-3">
      <article class="card p-6">
        <span class="text-sm font-bold text-emerald-600">01</span>
        <h2 class="mt-3 text-lg font-bold text-slate-900">微信扫码</h2>
        <p class="mt-2 text-sm leading-6 text-slate-500">使用微信扫一扫识别小程序码，进入报名首页。</p>
      </article>
      <article class="card p-6">
        <span class="text-sm font-bold text-emerald-600">02</span>
        <h2 class="mt-3 text-lg font-bold text-slate-900">选择赛事与组别</h2>
        <p class="mt-2 text-sm leading-6 text-slate-500">在小程序内选择赛事、组别，填写报名资料。</p>
      </article>
      <article class="card p-6">
        <span class="text-sm font-bold text-emerald-600">03</span>
        <h2 class="mt-3 text-lg font-bold text-slate-900">支付并完成报名</h2>
        <p class="mt-2 text-sm leading-6 text-slate-500">在线完成支付，报名结果以小程序内记录为准。</p>
      </article>
    </div>

    <div class="mt-10 flex flex-wrap items-center gap-4">
      <RouterLink v-if="event" :to="`/events/${event.id}`" class="btn-primary">返回赛事详情</RouterLink>
      <RouterLink v-else to="/" class="btn-primary">返回赛事列表</RouterLink>
      <RouterLink v-if="selectedGroup && event" :to="`/events/${event.id}#groups`" class="btn-secondary">
        查看其他组别
      </RouterLink>
    </div>

    <div v-if="selectedGroup" class="mt-8 rounded-2xl bg-slate-100 p-5 text-sm text-slate-600">
      <p class="font-semibold text-slate-900">已选组别信息</p>
      <p class="mt-2">
        {{ selectedGroup.name }} · {{ formatPrice(selectedGroup.price) }} · 开始时间
        {{ formatDateTime(selectedGroup.start_time) }} · 剩余 {{ selectedGroup.remaining_slots }} 个名额
      </p>
      <p class="mt-1 text-xs text-slate-400">组别 ID：{{ selectedGroup.id }}</p>
    </div>

    <div v-if="loading" class="mt-6 text-sm text-slate-400">正在加载赛事信息…</div>
  </section>
</template>
