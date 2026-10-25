<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'
</script>

<template>
  <div class="flex min-h-screen flex-col">
    <header class="sticky top-0 z-30 border-b border-slate-200/80 bg-white/85 backdrop-blur">
      <div class="container-page flex h-16 items-center justify-between">
        <RouterLink to="/" class="flex items-center gap-3">
          <span class="flex h-9 w-9 items-center justify-center rounded-xl bg-emerald-600 text-sm font-black tracking-tight text-white">
            BIKE
          </span>
          <span class="text-base font-bold tracking-tight text-slate-900">骑行赛事</span>
        </RouterLink>

        <nav class="flex items-center gap-5 text-sm font-medium text-slate-600">
          <RouterLink to="/" class="transition hover:text-emerald-700">赛事列表</RouterLink>
          <RouterLink
            to="/enroll-guide"
            class="hidden rounded-full border border-slate-300 px-4 py-1.5 text-xs font-semibold text-slate-700 transition hover:border-emerald-400 hover:text-emerald-700 sm:inline-flex"
          >
            小程序报名指引
          </RouterLink>
        </nav>
      </div>
    </header>

    <main class="flex-1">
      <RouterView />
    </main>

    <footer class="border-t border-slate-200 bg-white">
      <div class="container-page flex flex-col gap-3 py-8 text-sm text-slate-500 sm:flex-row sm:items-center sm:justify-between">
        <p>骑行赛事 · 报名请进入微信小程序</p>
        <p>官网仅展示赛事信息，不承载报名、支付等写操作。</p>
      </div>
    </footer>
  </div>
</template>
