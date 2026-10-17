// 后端模型类型定义（与 Go 结构体 json tag 保持一致）

export const ROLE_GUEST = 0;
export const ROLE_COMMON = 1;
export const ROLE_ADMIN = 10;
export const ROLE_ROOT = 100;

export const USER_STATUS_ENABLED = 1;
export const USER_STATUS_DISABLED = 2;

export interface User {
  id: number;
  username: string;
  display_name: string;
  role: number;
  status: number;
  email: string;
  github_id?: string;
  wechat_id?: string;
  lark_id?: string;
  oidc_id?: string;
  group: string;
  quota: number;
  used_quota: number;
  request_count: number;
  aff_code?: string;
  inviter_id?: number;
  tenant_id?: number;
}

export interface Token {
  id: number;
  user_id: number;
  key: string;
  status: number;
  name: string;
  created_time: number;
  accessed_time: number;
  expired_time: number;
  remain_quota: number;
  unlimited_quota: boolean;
  used_quota: number;
  models: string | null;
  subnet: string | null;
  rpm: number;
  tpm: number;
  concurrency: number;
  daily_quota_limit: number;
  monthly_quota_limit: number;
  allowed_modes: string | null;
}

export interface Channel {
  id: number;
  type: number;
  key: string;
  status: number;
  name: string;
  weight: number;
  created_time: number;
  test_time: number;
  response_time: number;
  balance: number;
  balance_updated_time: number;
  models: string;
  group: string;
  used_quota: number;
  priority: number;
  auto_ban: number;
  other: string;
  other_info?: string;
  setting?: string;
  tag?: string;
  base_url?: string;
  model_mapping?: string;
  status_code_mapping?: string;
  param_override?: string;
}

export interface Log {
  id: number;
  user_id: number;
  created_at: number;
  type: number;
  content: string;
  username: string;
  token_name: string;
  model_name: string;
  quota: number;
  prompt_tokens: number;
  completion_tokens: number;
  use_time: number;
  is_stream: boolean;
  channel: number;
  channel_name?: string;
  group?: string;
  other?: string;
}

export interface ChannelHealth {
  channel_id: number;
  name: string;
  type: number;
  status: number;
  request_count: number;
  quota: number;
  prompt_tokens: number;
  completion_tokens: number;
  avg_elapsed_time: number;
  success_rate: number;
  sample_count: number;
}

/** GET /api/channel/health 的响应体：条目包在 items 里 */
export interface ChannelHealthResponse {
  hours: number;
  items: ChannelHealth[];
}

export interface ModelCostStat {
  model_name: string;
  request_count: number;
  quota: number;
  cost_quota: number;
  prompt_tokens: number;
  completion_tokens: number;
}

export interface UserCostStat {
  user_id: number;
  username: string;
  request_count: number;
  quota: number;
  cost_quota: number;
}

export interface ChannelCostStat {
  channel_id: number;
  request_count: number;
  quota: number;
  cost_quota: number;
  prompt_tokens: number;
  completion_tokens: number;
  avg_elapsed_time: number;
}

export interface DailyCostStat {
  day: string;
  request_count: number;
  quota: number;
  cost_quota: number;
  tokens: number;
}

export interface CostAnalysis {
  hours: number;
  summary: {
    quota: number;
    request_count: number;
    prompt_tokens: number;
    completion_tokens: number;
    cost_quota: number;
    gross_profit: number;
    cost_configured: boolean;
  };
  models: ModelCostStat[];
  users: UserCostStat[];
  channels: ChannelCostStat[];
  daily: DailyCostStat[];
}

export interface Redemption {
  id: number;
  user_id: number;
  key: string;
  status: number;
  name: string;
  quota: number;
  created_time: number;
  redeemed_time: number;
  count: number;
  used_user_id: number;
}

export interface StatusInfo {
  version: string;
  start_time: number;
  email_verification: boolean;
  github_oauth: boolean;
  github_client_id: string;
  lark_client_id: string;
  system_name: string;
  logo: string;
  footer_html: string;
  wechat_qrcode: string;
  wechat_login: boolean;
  server_address: string;
  turnstile_check: boolean;
  turnstile_site_key: string;
  top_up_link: string;
  chat_link: string;
  quota_per_unit: number;
  display_in_currency: boolean;
  oidc: boolean;
  oidc_client_id: string;
}

export interface OptionItem {
  key: string;
  value: string;
}

export interface GroupInfo {
  name: string;
  ratio: number;
  desc?: string;
}

export interface DashboardEntry {
  day: string;
  model_name: string;
  quota: number;
  count: number;
  prompt_tokens: number;
  completion_tokens: number;
}

export const LOG_TYPE_ALL = 0;
export const LOG_TYPE_TOPUP = 1;
export const LOG_TYPE_CONSUME = 2;
export const LOG_TYPE_MANAGE = 3;
export const LOG_TYPE_SYSTEM = 4;

export const CHANNEL_STATUS_ENABLED = 1;
export const CHANNEL_STATUS_MANUALLY_DISABLED = 2;
export const CHANNEL_STATUS_AUTO_DISABLED = 3;
