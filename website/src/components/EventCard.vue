<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink } from 'vue-router'

import type { EventListItem } from '@/api/types'
import {
  formatDate,
  formatDateTime,
  formatDuration,
  formatPrice,
  isDeadlineSoon,
  statusClass,
  statusLabel,
} from '@/utils/format'

defineProps<{
  event: EventListItem
}>()

const imageFailed = ref(false)
</script>

<template>
  <RouterLink
    :to="`/events/${event.id}`"
    class="group card flex h-full flex-col overflow-hidden transition duration-300 hover:-translate-y-1 hover:shadow-xl"
  >
    <div class="relative h-44 overflow-hidden bg-gradient-to-br from-emerald-900 via-slate-800 to-slate-900">
      <img
        v-if="event.cover_url && !imageFailed"
        :src="event.cover_url"
        :alt="event.title"
        class="h-full w-full object-cover transition duration-500 group-hover:scale-105"
        loading="lazy"
        @error="imageFailed = true"
      />
      <div v-else class="flex h-full flex-col justify-end gap-1 p-5 text-white">
        <span class="text-[10px] font-semibold uppercase tracking-[0.24em] text-emerald-300">Bike Event</span>
        <span class="line-clamp-2 text-xl font-bold leading-tight">{{ event.title }}</span>
      </div>
      <span
        class="absolute left-4 top-4 rounded-full px-3 py-1 text-xs font-semibold ring-1"
        :class="statusClass(event.registration_status, event.lifecycle_status)"
      >
        {{ statusLabel(event.registration_status, event.lifecycle_status) }}
      </span>
    </div>

    <div class="flex flex-1 flex-col p-5">
      <h2 class="line-clamp-2 text-lg font-bold text-slate-900 group-hover:text-emerald-700">
        {{ event.title }}
      </h2>
      <div class="mt-3 space-y-2 text-sm text-slate-500">
        <p class="flex flex-wrap items-center gap-2">
          <span class="inline-block h-1.5 w-1.5 rounded-full bg-emerald-500" />
          {{ formatDate(event.start_time) }}
        </p>
        <p
          class="flex flex-wrap items-center gap-2"
          :class="isDeadlineSoon(event.registration_deadline) ? 'font-semibold text-rose-600' : ''"
        >
          <span class="inline-block h-1.5 w-1.5 rounded-full bg-amber-400" />
          报名截止：{{ event.registration_deadline ? formatDateTime(event.registration_deadline) : '—' }}
          <span
            v-if="isDeadlineSoon(event.registration_deadline)"
            class="rounded-full bg-rose-100 px-2 py-0.5 text-[11px] font-semibold text-rose-700"
          >
            即将截止
          </span>
        </p>
        <p class="flex flex-wrap items-center gap-2">
          <span class="inline-block h-1.5 w-1.5 rounded-full bg-slate-300" />
          持续：{{ formatDuration(event.duration_minutes) }}
        </p>
        <p class="flex items-center gap-2">
          <span class="inline-block h-1.5 w-1.5 rounded-full bg-slate-300" />
          {{ event.location }}
        </p>
      </div>
      <div class="mt-5 flex items-end justify-between border-t border-slate-100 pt-4">
        <div>
          <p class="text-xs text-slate-400">最低报名费</p>
          <p class="text-xl font-bold text-emerald-700">{{ formatPrice(event.min_price) }}</p>
        </div>
        <span class="text-sm font-semibold text-slate-500 transition group-hover:text-emerald-700">
          查看详情 →
        </span>
      </div>
    </div>
  </RouterLink>
</template>
