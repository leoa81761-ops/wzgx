import { createRouter, createWebHistory } from "vue-router";
import { loadSession, session } from "./session";
import Landing from "./views/Landing.vue";
import Login from "./views/Login.vue";
import Register from "./views/Register.vue";
import Legal from "./views/Legal.vue";
import Tutorial from "./views/Tutorial.vue";
import Dashboard from "./views/Dashboard.vue";
import Accounts from "./views/Accounts.vue";
import Plans from "./views/Plans.vue";
import Articles from "./views/Articles.vue";
import ArticleDetail from "./views/ArticleDetail.vue";
import Membership from "./views/Membership.vue";
import Admin from "./views/Admin.vue";
import Help from "./views/Help.vue";
import Pricing from "./views/Pricing.vue";
import Complaint from "./views/Complaint.vue";
import Inbox from "./views/Inbox.vue";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", component: Landing },
    { path: "/login", component: Login },
    { path: "/register", component: Register },
    { path: "/legal/:docType", component: Legal },
    { path: "/tutorial", component: Tutorial },
    { path: "/help", component: Help },
    { path: "/pricing", component: Pricing },
    { path: "/complaint", component: Complaint },
    { path: "/app", component: Dashboard, meta: { auth: true } },
    { path: "/app/accounts", component: Accounts, meta: { auth: true } },
    { path: "/app/plans", component: Plans, meta: { auth: true } },
    { path: "/app/articles", component: Articles, meta: { auth: true } },
    { path: "/app/articles/:id", component: ArticleDetail, meta: { auth: true } },
    { path: "/app/membership", component: Membership, meta: { auth: true } },
    { path: "/app/inbox", component: Inbox, meta: { auth: true } },
    { path: "/admin", component: Admin, meta: { auth: true, admin: true } },
  ],
  scrollBehavior() {
    return { top: 0 };
  },
});

router.beforeEach(async (to) => {
  if (!session.ready) await loadSession();
  if (to.meta.auth && !session.user) {
    return { path: "/login", query: { next: to.fullPath } };
  }
  if (to.meta.admin && !session.user?.is_admin) return "/app";
  return true;
});

export default router;
