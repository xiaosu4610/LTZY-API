import { useEffect, type ReactNode } from 'react';
import { Outlet, createRootRoute, createRoute, createRouter, redirect } from '@tanstack/react-router';
import { useAppStore } from '@/stores/app';
import { getData } from '@/lib/api';
import type { StatusInfo, User } from '@/lib/types';
import { Sidebar, Topbar } from '@/components/layout/shell';
import { ensurePermission } from '@/lib/permissions';
import { changeLang, readLang } from '@/i18n';

// ---- 顶层：拉取站点状态与当前登录用户 ----
function RootComponent() {
  return <Outlet />;
}

const rootRoute = createRootRoute({ component: RootComponent });

/** 登录态守卫：无用户则跳转登录页 */
async function ensureUser() {
  const store = useAppStore.getState();
  if (store.userLoaded && store.user) return;
  try {
    const user = await getData<User>('/api/user/self');
    if (user && user.id) {
      useAppStore.getState().setUser(user);
      return;
    }
  } catch {
    // 未登录
  }
  useAppStore.getState().setUser(null);
  throw redirect({ to: '/login' });
}

function ConsoleLayout() {
  return (
    <div className="flex min-h-screen">
      <Sidebar />
      <div className="flex min-w-0 flex-1 flex-col">
        <Topbar />
        <main className="ltzy-enter flex-1 px-4 py-6 sm:px-6 lg:px-8">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

/** 公开页布局：顶栏仅在登录后渲染，避免游客页出现空白 header */
function PublicLayout() {
  return (
    <div className="flex min-h-screen flex-col">
      <Outlet />
    </div>
  );
}

// ---- 页面组件 ----
import HomePage from '@/pages/home';
import AboutPage from '@/pages/about';
import LoginPage from '@/pages/login';
import RegisterPage from '@/pages/register';
import ResetPage from '@/pages/reset';
import NotFoundPage from '@/pages/not-found';
import DashboardPage from '@/pages/console/dashboard';
import TokensPage from '@/pages/console/tokens';
import ChannelsPage from '@/pages/console/channels';
import UsersPage from '@/pages/console/users';
import LogsPage from '@/pages/console/logs';
import OperationsPage from '@/pages/console/operations';
import RedemptionsPage from '@/pages/console/redemptions';
import TopupPage from '@/pages/console/topup';
import SettingPage from '@/pages/console/setting';
import OptionHistoryPage from '@/pages/console/option-history';
import MidjourneyPage from '@/pages/console/midjourney';
import ModelsPage from '@/pages/console/models';
import ProfilePage from '@/pages/console/profile';
import ForbiddenPage from '@/pages/console/forbidden';

const publicLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'public',
  component: PublicLayout,
});

const homeRoute = createRoute({
  getParentRoute: () => publicLayoutRoute,
  path: '/',
  component: HomePage,
});

const aboutRoute = createRoute({
  getParentRoute: () => publicLayoutRoute,
  path: '/about',
  component: AboutPage,
});

const loginRoute = createRoute({
  getParentRoute: () => publicLayoutRoute,
  path: '/login',
  component: LoginPage,
});

const registerRoute = createRoute({
  getParentRoute: () => publicLayoutRoute,
  path: '/register',
  component: RegisterPage,
});

const resetRoute = createRoute({
  getParentRoute: () => publicLayoutRoute,
  path: '/reset',
  component: ResetPage,
});

const consoleLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'console',
  beforeLoad: ensureUser,
  component: ConsoleLayout,
});

// 后台子路由统一挂在 ConsoleLayout（同一套侧边栏 + 顶栏）下，必须与侧边栏链接（shell.tsx）
// 保持一致，否则链接会落到 $ 兜底路由，表现为「点哪个页面都提示不存在」。
//
// 泛型 TPath 不能省：若写成 path: string，TanStack Router 会丢失字面量路径类型，
// 导致 navigate({ to: '/console' }) 之类调用报「不是合法路由」的类型错误。
function page<TPath extends string>(path: TPath, component: () => ReactNode) {
  return createRoute({
    getParentRoute: () => consoleLayoutRoute,
    path,
    component,
  });
}

// 管理区页面：进入前先校验权限点（在 beforeLoad 里 throw redirect，参考 ensureUser 的写法），
// 不具备权限时重定向到 403 页面，避免「点进去才被告知没有权限」。
function adminPage<TPath extends string>(path: TPath, perm: string, component: () => ReactNode) {
  return createRoute({
    getParentRoute: () => consoleLayoutRoute,
    path,
    beforeLoad: async () => {
      const allowed = await ensurePermission(perm);
      if (!allowed) throw redirect({ to: '/forbidden' });
    },
    component,
  });
}

// 旧 /console/* 管理路径 → 新 /admin/* 的兼容重定向，避免旧书签 404
type AdminPath =
  | '/admin/channels'
  | '/admin/users'
  | '/admin/redemptions'
  | '/admin/logs'
  | '/admin/operations'
  | '/admin/setting'
  | '/admin/option-history';

function legacyPage<TPath extends string>(path: TPath, to: AdminPath) {
  return createRoute({
    getParentRoute: () => consoleLayoutRoute,
    path,
    beforeLoad: () => {
      throw redirect({ to });
    },
  });
}

// ---- 用户区（所有登录用户，保留在原 /console 路径）----
const consoleIndexRoute = page('/console', DashboardPage);
const tokensRoute = page('/console/tokens', TokensPage);
const midjourneyRoute = page('/console/midjourney', MidjourneyPage);
const modelsRoute = page('/console/models', ModelsPage);
const topupRoute = page('/console/topup', TopupPage);
const profileRoute = page('/console/profile', ProfilePage);
const forbiddenRoute = page('/forbidden', ForbiddenPage);

// ---- 管理区（迁到 /admin 前缀，逐个权限点控制）----
const channelsRoute = adminPage('/admin/channels', 'channel.read', ChannelsPage);
const usersRoute = adminPage('/admin/users', 'user.read', UsersPage);
const redemptionsRoute = adminPage('/admin/redemptions', 'redemption.read', RedemptionsPage);
const logsRoute = adminPage('/admin/logs', 'log.read', LogsPage);
const operationsRoute = adminPage('/admin/operations', 'log.read', OperationsPage);
const settingRoute = adminPage('/admin/setting', 'option.read', SettingPage);
const optionHistoryRoute = adminPage('/admin/option-history', 'option.read', OptionHistoryPage);

// ---- 旧 /console/* 管理路径重定向 ----
const legacyChannelsRoute = legacyPage('/console/channels', '/admin/channels');
const legacyUsersRoute = legacyPage('/console/users', '/admin/users');
const legacyRedemptionsRoute = legacyPage('/console/redemptions', '/admin/redemptions');
const legacyLogsRoute = legacyPage('/console/logs', '/admin/logs');
const legacyOperationsRoute = legacyPage('/console/operations', '/admin/operations');
const legacySettingRoute = legacyPage('/console/setting', '/admin/setting');
const legacyOptionHistoryRoute = legacyPage('/console/option-history', '/admin/option-history');

const notFoundRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '$',
  component: NotFoundPage,
});

const routeTree = rootRoute.addChildren([
  publicLayoutRoute.addChildren([
    homeRoute,
    aboutRoute,
    loginRoute,
    registerRoute,
    resetRoute,
  ]),
  consoleLayoutRoute.addChildren([
    // 用户区
    consoleIndexRoute,
    tokensRoute,
    midjourneyRoute,
    modelsRoute,
    topupRoute,
    profileRoute,
    forbiddenRoute,
    // 管理区
    channelsRoute,
    usersRoute,
    redemptionsRoute,
    logsRoute,
    operationsRoute,
    settingRoute,
    optionHistoryRoute,
    // 旧路径重定向
    legacyChannelsRoute,
    legacyUsersRoute,
    legacyRedemptionsRoute,
    legacyLogsRoute,
    legacyOperationsRoute,
    legacySettingRoute,
    legacyOptionHistoryRoute,
  ]),
  notFoundRoute,
]);

export const router = createRouter({ routeTree, defaultPreload: 'intent' });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

/** 应用启动引导：站点信息 + 语言 + 登录态，供 main.tsx 调用 */
export function useBootstrap() {
  useEffect(() => {
    changeLang(readLang());
    void getData<StatusInfo>('/api/status')
      .then((status) => useAppStore.getState().setStatus(status))
      .catch(() => undefined);
    void getData<User>('/api/user/self')
      .then((user) => useAppStore.getState().setUser(user?.id ? user : null))
      .catch(() => useAppStore.getState().setUser(null));
  }, []);
}
