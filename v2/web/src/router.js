import { createRouter, createWebHistory } from 'vue-router'
import { session, loadMe, isStaff } from './store.js'

const U = (p) => () => import(`./views/${p}.vue`)
const A = (p) => () => import(`./admin/${p}.vue`)

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: U('Login'), meta: { public: true } },
    { path: '/register', component: U('Register'), meta: { public: true } },
    { path: '/terms', component: U('Terms'), meta: { public: true } },
    { path: '/privacy', component: U('Privacy'), meta: { public: true } },
    {
      path: '/', component: U('UserLayout'), meta: { role: 'user' },
      children: [
        { path: '', component: U('Home') },
        { path: 'accounts', component: U('Accounts') },
        { path: 'accounts/:id', component: U('AccountDetail') },
        { path: 'plans', component: U('Plans') },
        { path: 'articles', component: U('Articles') },
        { path: 'me', component: U('Me') },
      ],
    },
    {
      path: '/admin', component: A('AdminLayout'), meta: { role: 'staff' },
      children: [
        { path: '', redirect: '/admin/users' },
        { path: 'users', component: A('Users') },
        { path: 'accounts', component: A('Accounts') },
        { path: 'accounts/:id', component: A('AccountEdit') },
        { path: 'types', component: A('Types') },
        { path: 'configs', component: A('Configs') },
        { path: 'routes', component: A('Routes') },
        { path: 'operators', component: A('Operators'), meta: { superAdmin: true } },
        { path: 'sensitive', component: A('Sensitive') },
        { path: 'feedback', component: A('Feedback') },
        { path: 'settings', component: A('Settings') },
        { path: 'audit', component: A('Audit') },
      ],
    },
    { path: '/:rest(.*)*', redirect: '/' },
  ],
})

router.beforeEach(async (to) => {
  if (!session.loaded) await loadMe()
  if (to.meta.public) return true
  const me = session.me
  if (!me) return { path: '/login', query: { next: to.fullPath } }
  const want = to.matched.find((r) => r.meta.role)?.meta.role
  if (want === 'user' && isStaff(me)) return '/admin'
  if (want === 'staff' && !isStaff(me)) return '/'
  // operator pages are for the super admin only; operators who open the URL go back to the console home
  if (to.matched.some((r) => r.meta.superAdmin) && me.role !== 'super_admin') return { path: '/admin/users', query: { denied: '1' } }
  return true
})
