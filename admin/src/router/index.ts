import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/',
      redirect: '/events',
    },
    {
      path: '/events',
      name: 'events',
      component: () => import('@/views/EventListView.vue'),
      meta: { title: '赛事管理' },
    },
    {
      path: '/events/:eventId',
      name: 'event-detail',
      component: () => import('@/views/EventDetailView.vue'),
      meta: { title: '赛事详情' },
    },
    {
      path: '/enrollments',
      name: 'enrollments',
      component: () => import('@/views/EnrollmentListView.vue'),
      meta: { title: '报名名单' },
    },
    {
      path: '/stats',
      name: 'stats',
      component: () => import('@/views/StatsOverviewView.vue'),
      meta: { title: '统计看板' },
    },
  ],
})

router.afterEach((to) => {
  const title = to.meta.title
  document.title = typeof title === 'string' ? `${title} · 骑行赛事管理后台` : '骑行赛事管理后台'
})

export default router
