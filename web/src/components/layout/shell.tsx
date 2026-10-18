import { useEffect, useState } from 'react';
import { Link, useNavigate, useRouterState } from '@tanstack/react-router';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  Activity,
  BarChart3,
  BookOpen,
  Coins,
  Gauge,
  Globe,
  History,
  KeyRound,
  LayoutDashboard,
  LayoutGrid,
  LogOut,
  Menu,
  Moon,
  ScrollText,
  Server,
  Settings,
  Sun,
  Ticket,
  Users,
  Wallet,
  Wand2,
} from 'lucide-react';
import { useAppStore } from '@/stores/app';
import { api } from '@/lib/api';
import { resetPermissionCache, usePermissions } from '@/lib/permissions';
import { changeLang } from '@/i18n';
import { cn } from '@/lib/cn';
import { Button } from '@/components/ui/button';

interface NavItem {
  to: string;
  label: string;
  icon: React.ReactNode;
  /** 需要的权限点；缺省表示所有登录用户可见 */
  perm?: string;
}

export function Sidebar() {
  const { t } = useTranslation();
  const status = useAppStore((s) => s.status);
  const collapsed = useAppStore((s) => s.sidebarCollapsed);
  const toggleSidebar = useAppStore((s) => s.toggleSidebar);
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { hasPerm } = usePermissions();

  // 用户区：所有登录用户可见
  const userItems: NavItem[] = [
    { to: '/', label: t('nav.home'), icon: <Globe size={16} /> },
    { to: '/console', label: t('nav.dashboard'), icon: <LayoutDashboard size={16} /> },
    { to: '/console/models', label: t('nav.models'), icon: <LayoutGrid size={16} /> },
    { to: '/console/tokens', label: t('nav.tokens'), icon: <KeyRound size={16} /> },
    { to: '/console/midjourney', label: t('nav.midjourney'), icon: <Wand2 size={16} /> },
  ];

  // 管理区：按权限点逐项显隐；无任何管理权限时整块（含标题）不渲染
  const adminItems: NavItem[] = [
    { to: '/admin/channels', label: t('nav.channels'), icon: <Server size={16} />, perm: 'channel.read' },
    { to: '/admin/users', label: t('nav.users'), icon: <Users size={16} />, perm: 'user.read' },
    { to: '/admin/redemptions', label: t('nav.redemptions'), icon: <Ticket size={16} />, perm: 'redemption.read' },
    { to: '/admin/logs', label: t('nav.logs'), icon: <ScrollText size={16} />, perm: 'log.read' },
    { to: '/admin/operations', label: t('nav.operations'), icon: <Activity size={16} />, perm: 'log.read' },
    { to: '/admin/setting', label: t('nav.setting'), icon: <Settings size={16} />, perm: 'option.read' },
    { to: '/admin/option-history', label: t('nav.optionHistory'), icon: <History size={16} />, perm: 'option.read' },
  ].filter((i) => i.perm != null && hasPerm(i.perm));

  // 账户区
  const accountItems: NavItem[] = [
    { to: '/console/topup', label: t('nav.topup'), icon: <Wallet size={16} /> },
    { to: '/console/profile', label: t('nav.profile'), icon: <Coins size={16} /> },
    { to: '/about', label: t('nav.about'), icon: <BookOpen size={16} /> },
  ];

  const sections: { title?: string; items: NavItem[] }[] = [
    { items: userItems },
    ...(adminItems.length > 0 ? [{ title: t('nav.adminSection'), items: adminItems }] : []),
    { items: accountItems },
  ];

  return (
    <aside
      className={cn(
        'sticky top-0 z-30 flex h-screen shrink-0 flex-col border-r border-[var(--ltzy-border)]',
        'bg-[var(--ltzy-surface)]/80 backdrop-blur-xl transition-[width] duration-200',
        collapsed ? 'w-[68px]' : 'w-[232px]',
      )}
    >
      <div className="flex h-14 items-center gap-2 border-b border-[var(--ltzy-border)] px-4">
        <Link to="/" className="flex min-w-0 items-center gap-2">
          {status?.logo ? (
            <img src={status.logo} alt="logo" className="h-7 w-7 rounded-md object-cover" />
          ) : (
            <span className="flex h-7 w-7 items-center justify-center rounded-md bg-[var(--ltzy-primary)] text-white">
              <Gauge size={16} />
            </span>
          )}
          {!collapsed ? (
            <span className="truncate text-sm font-semibold tracking-tight">
              {status?.system_name || 'LTZY-API'}
            </span>
          ) : null}
        </Link>
      </div>

      <nav className="flex-1 overflow-y-auto px-2 py-3">
        {sections.map((section, si) => (
          <div key={si} className={cn(si > 0 && 'mt-4')}>
            {section.title && !collapsed ? (
              <div className="mb-1 px-2 text-[10px] font-semibold tracking-wider text-[var(--ltzy-muted)]/70 uppercase">
                {section.title}
              </div>
            ) : null}
            {section.items.map((item) => {
              const active =
                item.to === '/' ? pathname === '/' : pathname.startsWith(item.to);
              return (
                <Link
                  key={item.to}
                  to={item.to}
                  className={cn(
                    'mb-0.5 flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm transition-colors',
                    active
                      ? 'bg-[var(--ltzy-primary)]/12 font-medium text-[var(--ltzy-primary)]'
                      : 'text-[var(--ltzy-muted)] hover:bg-[var(--ltzy-surface-2)] hover:text-[var(--ltzy-fg)]',
                    collapsed && 'justify-center',
                  )}
                  title={collapsed ? item.label : undefined}
                >
                  {item.icon}
                  {!collapsed ? <span className="truncate">{item.label}</span> : null}
                </Link>
              );
            })}
          </div>
        ))}
      </nav>

      <div className="border-t border-[var(--ltzy-border)] p-2">
        <button
          onClick={toggleSidebar}
          className={cn(
            'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-sm text-[var(--ltzy-muted)]',
            'hover:bg-[var(--ltzy-surface-2)] hover:text-[var(--ltzy-fg)]',
            collapsed && 'justify-center',
          )}
        >
          <Menu size={16} />
          {!collapsed ? <span>Collapse</span> : null}
        </button>
      </div>
    </aside>
  );
}

export function Topbar() {
  const { t, i18n } = useTranslation();
  const user = useAppStore((s) => s.user);
  const theme = useAppStore((s) => s.theme);
  const toggleTheme = useAppStore((s) => s.toggleTheme);
  const quotaPerUnit = useAppStore((s) => s.quotaPerUnit());
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [menuOpen, setMenuOpen] = useState(false);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(id);
  }, []);

  if (!user) return null;

  const signOut = async () => {
    await api.get('/api/user/logout').catch(() => undefined);
    // 清除权限缓存，避免换号登录后沿用上一账号的菜单权限
    resetPermissionCache();
    queryClient.removeQueries({ queryKey: ['permissions'] });
    useAppStore.getState().setUser(null);
    void navigate({ to: '/login' });
  };

  return (
    <header className="sticky top-0 z-20 flex h-14 items-center justify-between gap-4 border-b border-[var(--ltzy-border)] bg-[var(--ltzy-surface)]/80 px-4 backdrop-blur-xl sm:px-6">
      <div className="flex items-center gap-2 text-xs text-[var(--ltzy-muted)]">
        <BarChart3 size={14} />
        <span className="hidden sm:inline">{t('common.total', { count: user.request_count })}</span>
      </div>

      <div className="flex items-center gap-1.5">
        <span className="mr-1 hidden rounded-md border border-[var(--ltzy-border)] px-2 py-1 font-mono text-xs sm:inline">
          ¥{((user.quota || 0) / quotaPerUnit).toFixed(4)}
        </span>
        <Button
          variant="ghost"
          size="icon"
          onClick={() => {
            void changeLang(i18n.language === 'zh' ? 'en' : 'zh');
          }}
          title="language"
          className="font-mono text-[11px]"
        >
          {i18n.language === 'zh' ? '中' : 'EN'}
        </Button>
        <Button variant="ghost" size="icon" onClick={toggleTheme} title="theme">
          {theme === 'dark' ? <Sun size={16} /> : <Moon size={16} />}
        </Button>

        <div className="relative">
          <button
            onClick={() => setMenuOpen((v) => !v)}
            className="flex items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-[var(--ltzy-surface-2)]"
          >
            <span className="flex h-7 w-7 items-center justify-center rounded-full bg-[var(--ltzy-primary)]/15 text-xs font-semibold text-[var(--ltzy-primary)] uppercase">
              {(user.display_name || user.username).slice(0, 1)}
            </span>
            <span className="hidden text-sm sm:inline">{user.display_name || user.username}</span>
          </button>
          {menuOpen ? (
            <>
              <div className="fixed inset-0 z-10" onClick={() => setMenuOpen(false)} />
              <div className="absolute right-0 z-20 mt-1 w-44 overflow-hidden rounded-lg border border-[var(--ltzy-border)] bg-[var(--ltzy-surface)] py-1 shadow-xl">
                <Link
                  to="/console/profile"
                  className="block px-3 py-2 text-sm hover:bg-[var(--ltzy-surface-2)]"
                  onClick={() => setMenuOpen(false)}
                >
                  {t('nav.profile')}
                </Link>
                <button
                  onClick={signOut}
                  className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-[var(--ltzy-danger)] hover:bg-[var(--ltzy-surface-2)]"
                >
                  <LogOut size={14} />
                  {t('common.logout')}
                </button>
              </div>
            </>
          ) : null}
        </div>
        <span className="sr-only">{now}</span>
      </div>
    </header>
  );
}
