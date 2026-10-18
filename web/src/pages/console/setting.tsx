import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { toast } from 'sonner';
import dayjs from 'dayjs';
import { RotateCcw, Save } from 'lucide-react';
import { getData, mutate } from '@/lib/api';
import { useAppStore } from '@/stores/app';
import type { OptionItem } from '@/lib/types';
import { Button } from '@/components/ui/button';
import { Badge, Card, CardBody, CardHeader, Skeleton } from '@/components/ui/card';
import { Input, Textarea } from '@/components/ui/input';
import { PageHeader } from '@/components/ui/page';

/** 常用配置项的中文说明：这是给运维看的人话，不是代码里的键名 */
const OPTION_LABELS: Record<string, { label: string; desc?: string; multiline?: boolean }> = {
  SystemName: { label: '系统名称', desc: '显示在浏览器标题与页面左上角' },
  Logo: { label: 'Logo 地址', desc: '留空使用内置图标' },
  Notice: { label: '站点公告', desc: '显示在首页顶部，支持纯文本', multiline: true },
  About: { label: '关于页面内容', desc: '支持 Markdown / HTML', multiline: true },
  HomePageContent: { label: '首页自定义内容', desc: '显示在首页能力卡片下方', multiline: true },
  Footer: { label: '页脚自定义内容', desc: '支持 HTML，留空显示默认版权', multiline: true },
  ServerAddress: { label: '服务器公开地址', desc: '用于首页示例命令与文档链接' },
  QuotaPerUnit: { label: '每元额度', desc: '1 元人民币折算多少 quota，改动会影响全部展示金额（默认 1000000）' },
  RetryTimes: { label: '失败重试次数', desc: '上游失败后自动切换渠道重试的次数' },
  TopUpLink: { label: '充值页面链接', desc: '留空则隐藏在线充值入口' },
  ChatLink: { label: '对话页面链接', desc: '留空则隐藏聊天入口' },
  EmailVerificationEnabled: { label: '邮箱验证', desc: '注册时是否需要邮箱验证码' },
  EmailCodeLoginEnabled: { label: '邮箱验证码登录', desc: '开启后用户可用「邮箱 + 验证码」免密登录（依赖邮件服务可用）' },
  GitHubOAuthEnabled: { label: 'GitHub 登录', desc: '开启后登录页显示 GitHub 按钮' },
  GitHubClientId: { label: 'GitHub Client ID' },
  GitHubClientSecret: { label: 'GitHub Client Secret', desc: '敏感信息，仅管理员可见' },
  TurnstileCheckEnabled: { label: 'Turnstile 人机验证', desc: '注册与发信前的人机校验' },
  TurnstileSiteKey: { label: 'Turnstile Site Key' },
  TurnstileSecretKey: { label: 'Turnstile Secret Key', desc: '敏感信息，仅管理员可见' },
  MemoryCacheEnabled: { label: '内存缓存', desc: '开启后渠道与配置走内存缓存，减少数据库压力' },
  CanaryEnabled: { label: '灰度路由', desc: '把部分流量导向指定渠道，用于新上游试跑' },
  CanaryChannelId: { label: '灰度渠道 ID' },
  CanaryPercent: { label: '灰度流量比例', desc: '0-100，表示多少百分比的流量走灰度渠道' },
  CanaryGroups: { label: '灰度生效分组', desc: '逗号分隔，留空不限制' },
  ReasoningRatio: { label: '推理 Token 倍率', desc: 'JSON，如 {"o1":2.0}' },
  TieredRatio: { label: '阶梯定价', desc: 'JSON，如 {"gpt-4o":[{"threshold":0,"ratio":1.0}]}' },
  MidjourneyEnabled: { label: 'Midjourney 支持' },
  MidjourneyApiKey: { label: 'Midjourney API Key', desc: '敏感信息' },
  WebhookURL: { label: '通用 Webhook 通知' },
  LarkWebhookURL: { label: '飞书机器人 Webhook' },
  DingTalkWebhookURL: { label: '钉钉机器人 Webhook' },
  DingTalkSecret: { label: '钉钉加签密钥', desc: '敏感信息' },
  SlackWebhookURL: { label: 'Slack Webhook' },
  DefaultUserDailyQuotaLimit: { label: '新用户每日额度上限', desc: '0 表示不限制' },
  DefaultUserMonthlyQuotaLimit: { label: '新用户每月额度上限', desc: '0 表示不限制' },
  RolePermissions: { label: '角色权限表', desc: 'JSON，留空使用内置默认权限', multiline: true },
  // 渠道自动禁用语义与保活（LTZY-API 新增）
  RateLimitStatusCodes: { label: '限流状态码', desc: '逗号分隔。命中这些状态码时，只有响应体明确表示「密钥失效/欠费」才会禁用渠道；英伟达等渠道 429 只是限流，不会判死（默认 429）' },
  ChannelKeepAliveEnabled: { label: '渠道保活检测', desc: '开启后定时把「已被禁用但上游已恢复」的渠道自动救回来' },
  ChannelKeepAliveMinutes: { label: '保活检测周期（分钟）', desc: '默认 10 分钟' },
  ChannelKeepAliveScope: { label: '保活检测范围', desc: 'disabled = 只测已禁用的渠道（推荐，开销小）；all = 测全部渠道' },
  ChannelPresets: { label: '渠道预设', desc: 'JSON 数组，留空使用内置预设；填写则整体覆盖，用于自助维护上游地址与常用模型', multiline: true },
  // 在线充值（LTZY-API 新增）
  EpayEnabled: { label: '在线充值（易支付）', desc: '开启后充值页出现「在线充值」，用户可直接在线支付' },
  EpayApiUrl: { label: '易支付网关地址', desc: '例如 https://xnoo.cn/（不带 mapi.php）' },
  EpayPid: { label: '易支付商户 ID' },
  EpayMerchantPrivateKey: { label: '易支付商户私钥', desc: '敏感信息，仅管理员可见；用于下单签名。支持 PKCS#8/PKCS#1，带不带 PEM 头尾都行' },
  EpayPlatformPublicKey: { label: '易支付平台公钥', desc: '用于异步通知验签。强烈建议填写，否则无法验证通知真伪' },
  EpaySignType: { label: '签名算法', desc: 'RSA（实际为 SHA256withRSA）或 MD5，按支付平台文档选择' },
  EpayDefaultType: { label: '默认支付方式', desc: 'alipay / wxpay / qqpay' },
  EpayMinTopup: { label: '单笔最低充值（元）', desc: '默认 0.01（人民币最小单位）。低于该金额的充值请求会被拒绝' },
  EpaySitename: { label: '网站名称（回传给支付平台）', desc: '部分平台要求，可留空' },
  EpayAlipayFeeRate: { label: '支付宝费率', desc: '0~1 的小数，0.05 表示 5%。下单时固化到订单，用于核算实收' },
  EpayWxpayFeeRate: { label: '微信费率', desc: '0~1 的小数，0.06 表示 6%。下单时固化到订单，用于核算实收' },
  EpayFeeDeductFromQuota: { label: '手续费由用户承担', desc: '关闭（默认）= 用户充 100 到账 100，手续费平台承担；开启 = 按实收金额到账（微信充 100 到账 94）' },
};

const GROUP_KEYS: Record<string, string[]> = {
  '站点展示': ['SystemName', 'Logo', 'ServerAddress', 'Notice', 'HomePageContent', 'About', 'Footer'],
  '计费与额度': [
    'QuotaPerUnit',
    'RetryTimes',
    'ReasoningRatio',
    'TieredRatio',
    'DefaultUserDailyQuotaLimit',
    'DefaultUserMonthlyQuotaLimit',
    'TopUpLink',
    'ChatLink',
  ],
  '登录与安全': [
    'EmailVerificationEnabled',
    'EmailCodeLoginEnabled',
    'GitHubOAuthEnabled',
    'GitHubClientId',
    'GitHubClientSecret',
    'TurnstileCheckEnabled',
    'TurnstileSiteKey',
    'TurnstileSecretKey',
    'RolePermissions',
  ],
  '运行与调度': ['MemoryCacheEnabled', 'CanaryEnabled', 'CanaryChannelId', 'CanaryPercent', 'CanaryGroups'],
  '渠道与限流': [
    'RateLimitStatusCodes',
    'ChannelKeepAliveEnabled',
    'ChannelKeepAliveMinutes',
    'ChannelKeepAliveScope',
    'ChannelPresets',
  ],
  '在线充值': [
    'EpayEnabled',
    'EpayApiUrl',
    'EpayPid',
    'EpayMerchantPrivateKey',
    'EpayPlatformPublicKey',
    'EpaySignType',
    'EpayDefaultType',
    'EpayMinTopup',
    'EpaySitename',
    'EpayAlipayFeeRate',
    'EpayWxpayFeeRate',
    'EpayFeeDeductFromQuota',
  ],
  '通知渠道': ['WebhookURL', 'LarkWebhookURL', 'DingTalkWebhookURL', 'DingTalkSecret', 'SlackWebhookURL'],
  'Midjourney': ['MidjourneyEnabled', 'MidjourneyApiKey'],
};

export default function SettingPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const user = useAppStore((s) => s.user);
  const [values, setValues] = useState<Record<string, string>>({});
  const [dirty, setDirty] = useState<Record<string, boolean>>({});

  const { data, isLoading } = useQuery({
    queryKey: ['options'],
    queryFn: () => getData<OptionItem[]>('/api/option/'),
  });

  const { data: restartData } = useQuery({
    queryKey: ['restart-required'],
    // 后端返回 { items, count }，之前当成裸数组用会让整页 .some 崩掉
    queryFn: () => getData<{ items: string[]; count: number }>('/api/option/restart_required'),
  });
  const restartRequired = restartData?.items ?? [];

  useEffect(() => {
    if (!data) return;
    const next: Record<string, string> = {};
    for (const item of data) {
      next[item.key] = item.value;
    }
    setValues(next);
  }, [data]);

  const known = useMemo(() => new Set(Object.values(GROUP_KEYS).flat()), []);

  const save = async (key: string) => {
    await mutate('/api/option/', 'put', { key, value: values[key] ?? '' });
    setDirty((d) => ({ ...d, [key]: false }));
    toast.success(t('setting.saved'));
    await queryClient.invalidateQueries({ queryKey: ['options'] });
  };

  if (user && user.role < 100) {
    return (
      <div>
        <PageHeader title={t('setting.title')} desc={t('setting.subtitle')} />
        <Card>
          <CardBody>
            <p className="text-sm text-[var(--ltzy-muted)]">仅超级管理员可以访问系统设置。</p>
          </CardBody>
        </Card>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title={t('setting.title')}
        desc={t('setting.subtitle')}
        actions={
          restartRequired && restartRequired.length > 0 ? (
            <Badge tone="warning">{t('setting.restartRequired')}</Badge>
          ) : null
        }
      />

      {isLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-56 w-full" />
          <Skeleton className="h-56 w-full" />
        </div>
      ) : (
        <div className="space-y-4">
          {Object.entries(GROUP_KEYS).map(([group, keys]) => (
            <Card key={group}>
              <CardHeader
                title={group}
                extra={
                  restartRequired?.some((k) => keys.includes(k)) ? (
                    <Badge tone="warning">部分项需重启</Badge>
                  ) : null
                }
              />
              <CardBody className="grid gap-4 md:grid-cols-2">
                {keys
                  .filter((k) => k in values)
                  .map((key) => {
                    const meta = OPTION_LABELS[key] ?? { label: key };
                    const isDirty = dirty[key];
                    return (
                      <div key={key} className={meta.multiline ? 'md:col-span-2' : ''}>
                        <div className="mb-1.5 flex items-center justify-between gap-2">
                          <label className="text-xs font-medium text-[var(--ltzy-muted)]" title={key}>
                            {meta.label}
                            <span className="ml-1.5 font-mono text-[10px] opacity-50">{key}</span>
                          </label>
                          {isDirty ? (
                            <Button size="sm" variant="primary" onClick={() => void save(key)} icon={<Save size={12} />}>
                              {t('common.save')}
                            </Button>
                          ) : null}
                        </div>
                        {meta.multiline ? (
                          <Textarea
                            className="font-mono text-xs"
                            value={values[key] ?? ''}
                            onChange={(e) => {
                              setValues((v) => ({ ...v, [key]: e.target.value }));
                              setDirty((d) => ({ ...d, [key]: true }));
                            }}
                          />
                        ) : (
                          <div className="flex gap-2">
                            <Input
                              value={values[key] ?? ''}
                              onChange={(e) => {
                                setValues((v) => ({ ...v, [key]: e.target.value }));
                                setDirty((d) => ({ ...d, [key]: true }));
                              }}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter') void save(key);
                              }}
                            />
                            {!isDirty ? (
                              <Button
                                size="icon"
                                variant="ghost"
                                onClick={() => void save(key)}
                                title={t('common.save')}
                              >
                                <Save size={14} />
                              </Button>
                            ) : null}
                          </div>
                        )}
                        {meta.desc ? <p className="mt-1 text-[11px] text-[var(--ltzy-muted)]">{meta.desc}</p> : null}
                      </div>
                    );
                  })}
              </CardBody>
            </Card>
          ))}

          <Card>
            <CardHeader
              title="其他配置"
              desc="未归类的配置项，谨慎修改"
              extra={<RotateCcw size={14} className="text-[var(--ltzy-muted)]" />}
            />
            <CardBody className="space-y-3">
              {Object.keys(values)
                .filter((k) => !known.has(k))
                .map((key) => (
                  <div key={key} className="flex items-center gap-2">
                    <span className="w-64 shrink-0 truncate font-mono text-xs text-[var(--ltzy-muted)]" title={key}>
                      {key}
                    </span>
                    <Input
                      value={values[key] ?? ''}
                      onChange={(e) => {
                        setValues((v) => ({ ...v, [key]: e.target.value }));
                        setDirty((d) => ({ ...d, [key]: true }));
                      }}
                    />
                    <Button size="sm" variant="secondary" onClick={() => void save(key)}>
                      {t('common.save')}
                    </Button>
                  </div>
                ))}
            </CardBody>
          </Card>

          <p className="text-center text-[11px] text-[var(--ltzy-muted)]">
            最后加载：{dayjs().format('YYYY-MM-DD HH:mm')} · 敏感项以脱敏形式返回
          </p>
        </div>
      )}
    </div>
  );
}
