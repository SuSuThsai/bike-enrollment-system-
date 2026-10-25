import { createRouter, createWebHistory } from 'vue-router'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  scrollBehavior(to) {
    if (to.hash) {
      return { el: to.hash, behavior: 'smooth' }
    }
    return { top: 0 }
  },
  routes: [
    {
      path: '/',
      name: 'events',
      component: () => import('@/views/EventListView.vue'),
      meta: { title: '赛事列表' },
    },
    {
      path: '/events/:eventId',
      name: 'event-detail',
      component: () => import('@/views/EventDetailView.vue'),
      meta: { title: '赛事详情' },
    },
    {
      path: '/enroll-guide',
      name: 'enroll-guide',
      component: () => import('@/views/EnrollGuideView.vue'),
      meta: { title: '小程序报名指引' },
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue'),
      meta: { title: '页面不存在' },
    },
  ],
})

router.afterEach((to) => {
  const title = to.meta.title
  document.title = typeof title === 'string' ? `${title} · 骑行赛事` : '骑行赛事'
})

export default router
