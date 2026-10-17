import { useEffect, type ReactNode } from 'react';
import { Outlet, createRootRoute, createRoute, createRouter, redirect } from '@tanstack/react-router';
import { useAppStore } from '@/stores/app';
import { getData } from '@/lib/api';
import type { StatusInfo, User } from '@/lib/types';
import { Sidebar, Topbar } from '@/components/layout/shell';
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
import ProfilePage from '@/pages/console/profile';

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

function page(path: string, component: () => ReactNode) {
  return createRoute({
    getParentRoute: () => consoleLayoutRoute,
    path,
    component,
  });
}

const consoleIndexRoute = page('/', DashboardPage);
const tokensRoute = page('/tokens', TokensPage);
const channelsRoute = page('/channels', ChannelsPage);
const usersRoute = page('/users', UsersPage);
const logsRoute = page('/logs', LogsPage);
const operationsRoute = page('/operations', OperationsPage);
const redemptionsRoute = page('/redemptions', RedemptionsPage);
const topupRoute = page('/topup', TopupPage);
const settingRoute = page('/setting', SettingPage);
const profileRoute = page('/profile', ProfilePage);

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
    consoleIndexRoute,
    tokensRoute,
    channelsRoute,
    usersRoute,
    logsRoute,
    operationsRoute,
    redemptionsRoute,
    topupRoute,
    settingRoute,
    profileRoute,
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
