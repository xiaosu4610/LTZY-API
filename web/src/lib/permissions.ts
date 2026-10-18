import { useQuery } from '@tanstack/react-query';
import { getData } from '@/lib/api';

// 基于权限点的前端权限（LTZY-API C4）。
//
// 后端接口：GET /api/permission（需登录）
// 响应结构（见 src/controller/permission.go）：
//   { success: true, message: "", data: { all: string[], self: string[], role: number } }
//   - all ：全部可选权限点（管理员配置角色时用）
//   - self：当前登录者的权限点，前端据此控制菜单/页面显隐
//   - role：当前登录者的角色数值（1 普通 / 10 管理 / 100 超管）
export interface PermissionPayload {
  all: string[];
  self: string[];
  role: number;
}

// 通配权限点：拥有它视为拥有一切权限（与后端 model.PermAll 一致）
export const PERM_ALL = '*';

// 超管角色数值：与后端一致，超管恒定拥有全部权限
const ROLE_ROOT = 100;

// 模块级缓存：usePermissions（hook）与路由守卫 beforeLoad（无法使用 hooks）共用同一份请求，
// 避免同一会话内重复请求；退出登录时调用 resetPermissionCache 清除，防止换号后沿用旧权限。
let payloadPromise: Promise<PermissionPayload> | null = null;

function loadPermissionPayload(): Promise<PermissionPayload> {
  if (!payloadPromise) {
    payloadPromise = getData<PermissionPayload>('/api/permission')
      .then((payload) => payload ?? { all: [], self: [], role: 0 })
      .catch(() => {
        // 未登录或请求失败：视为无任何管理权限，并允许下次重试
        payloadPromise = null;
        return { all: [], self: [], role: 0 };
      });
  }
  return payloadPromise;
}

/** 判断给定权限集合是否命中目标权限点（支持通配符 *） */
export function matchPermission(payload: PermissionPayload, perm: string): boolean {
  // 超管恒定拥有全部权限，与后端 HasPermission 的硬编码底线保持一致
  if (payload.role >= ROLE_ROOT) return true;
  return payload.self.includes(PERM_ALL) || payload.self.includes(perm);
}

/** 退出登录时清除缓存，避免换号后沿用旧权限 */
export function resetPermissionCache() {
  payloadPromise = null;
}

/**
 * 当前登录用户的权限点。
 * 结果用 TanStack Query 缓存，key 为 ['permissions']，staleTime 取较长值。
 */
export function usePermissions() {
  const { data, isLoading } = useQuery({
    queryKey: ['permissions'],
    queryFn: loadPermissionPayload,
    staleTime: 10 * 60_000,
  });

  const permissions = data?.self ?? [];
  const hasPerm = (perm: string) => (data ? matchPermission(data, perm) : false);

  return { permissions, hasPerm, isLoading };
}

/** 供路由守卫（beforeLoad）使用：返回当前用户是否具备某权限点 */
export async function ensurePermission(perm: string): Promise<boolean> {
  const payload = await loadPermissionPayload();
  return matchPermission(payload, perm);
}
