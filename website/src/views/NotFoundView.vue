<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <section class="container-page flex min-h-[60vh] flex-col items-center justify-center py-24 text-center">
    <p class="text-sm font-bold uppercase tracking-[0.3em] text-emerald-600">404</p>
    <h1 class="mt-4 text-3xl font-black tracking-tight text-slate-900 sm:text-4xl">页面不存在</h1>
    <p class="mt-4 max-w-md text-slate-500">链接可能已失效，返回赛事列表继续浏览。</p>
    <RouterLink to="/" class="btn-primary mt-8">返回赛事列表</RouterLink>
  </section>
</template>
