import { create } from 'zustand';
import type { StatusInfo, User } from '@/lib/types';

type ThemeMode = 'dark' | 'light';

interface AppState {
  status: StatusInfo | null;
  user: User | null;
  userLoaded: boolean;
  theme: ThemeMode;
  sidebarCollapsed: boolean;
  setStatus: (status: StatusInfo) => void;
  setUser: (user: User | null) => void;
  setUserLoaded: (loaded: boolean) => void;
  quotaPerUnit: () => number;
  isAdmin: () => boolean;
  isRoot: () => boolean;
  toggleTheme: () => void;
  setTheme: (theme: ThemeMode) => void;
  toggleSidebar: () => void;
}

const THEME_KEY = 'ltzy-theme';

function readTheme(): ThemeMode {
  const saved = localStorage.getItem(THEME_KEY);
  return saved === 'light' ? 'light' : 'dark';
}

function applyTheme(theme: ThemeMode) {
  document.documentElement.classList.toggle('light', theme === 'light');
  localStorage.setItem(THEME_KEY, theme);
}

export const useAppStore = create<AppState>((set, get) => ({
  status: null,
  user: null,
  userLoaded: false,
  theme: readTheme(),
  sidebarCollapsed: false,
  setStatus: (status) => {
    set({ status });
    document.title = status.system_name || 'LTZY-API';
  },
  setUser: (user) => set({ user, userLoaded: true }),
  setUserLoaded: (userLoaded) => set({ userLoaded }),
  quotaPerUnit: () => get().status?.quota_per_unit || 500000,
  isAdmin: () => (get().user?.role ?? 0) >= 10,
  isRoot: () => (get().user?.role ?? 0) >= 100,
  toggleTheme: () => {
    const next: ThemeMode = get().theme === 'dark' ? 'light' : 'dark';
    applyTheme(next);
    set({ theme: next });
  },
  setTheme: (theme) => {
    applyTheme(theme);
    set({ theme });
  },
  toggleSidebar: () => set({ sidebarCollapsed: !get().sidebarCollapsed }),
}));

// 首屏立刻套用主题，避免闪白
applyTheme(readTheme());
