import axios, { AxiosError } from 'axios';

// 统一请求实例：与后端同源，靠 cookie 维持会话（One API 的 sessions 机制）。
export const api = axios.create({
  baseURL: '/',
  withCredentials: true,
  timeout: 60000,
});

export interface ApiResponse<T = unknown> {
  success: boolean;
  message: string;
  data?: T;
}

export class ApiError extends Error {
  status: number;

  constructor(message: string, status = 0) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

let errorHandler: ((message: string) => void) | null = null;

/** 由 App 注入全局错误提示（toast），避免本模块直接依赖 UI 库 */
export function setApiErrorHandler(handler: (message: string) => void) {
  errorHandler = handler;
}

function shouldReport(url?: string) {
  if (!url) return true;
  // 登录态探测、状态查询失败不需要打扰用户
  return !['/api/status', '/api/user/self', '/api/notice', '/api/about'].some((p) =>
    url.startsWith(p),
  );
}

api.interceptors.response.use(
  (response) => {
    const body = response.data as ApiResponse | undefined;
    if (body && typeof body === 'object' && 'success' in body && body.success === false) {
      const message = body.message || '请求失败';
      if (shouldReport(response.config.url)) {
        errorHandler?.(message);
      }
      throw new ApiError(message, response.status);
    }
    return response;
  },
  (error: AxiosError<ApiResponse>) => {
    const status = error.response?.status ?? 0;
    let message = error.response?.data?.message;
    if (!message) {
      message = status === 401 ? '未登录或登录已过期' : error.message || '网络错误';
    }
    if (shouldReport(error.config?.url)) {
      errorHandler?.(message);
    }
    return Promise.reject(new ApiError(message, status));
  },
);

/** 直接把 data 剥出来，业务代码少写一层 */
export async function getData<T>(url: string, params?: Record<string, unknown>): Promise<T> {
  const res = await api.get<ApiResponse<T>>(url, { params });
  return res.data.data as T;
}

export async function postData<T>(url: string, body?: unknown): Promise<T> {
  const res = await api.post<ApiResponse<T>>(url, body);
  return res.data.data as T;
}

export async function putData<T>(url: string, body?: unknown): Promise<T> {
  const res = await api.put<ApiResponse<T>>(url, body);
  return res.data.data as T;
}

export async function deleteData<T>(url: string): Promise<T> {
  const res = await api.delete<ApiResponse<T>>(url);
  return res.data.data as T;
}

/** 只关心 success/message 的写操作（也允许 get —— 后端有些「发信」类接口就是 GET） */
export async function mutate(url: string, method: 'get' | 'post' | 'put' | 'delete', body?: unknown) {
  const res = await api.request<ApiResponse>({ url, method, data: body });
  return res.data;
}
