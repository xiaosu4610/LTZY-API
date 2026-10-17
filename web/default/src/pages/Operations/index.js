import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Card,
  Dropdown,
  Grid,
  Header,
  Label,
  Message,
  Segment,
  Statistic,
  Table,
} from 'semantic-ui-react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';

import { API, showError } from '../../helpers';
import { renderQuota } from '../../helpers/render';

// 运营看板（LTZY-API 新增）
// 汇总两个只读接口：
//   - /api/log/cost          成本分析（总额、模型、用户、渠道、日趋势）
//   - /api/channel/health    渠道健康度（请求数、额度、成功率、平均耗时）

// 时间窗口选项：只存词条键，文案在组件内翻译（模块级拿不到 t）
const HOUR_OPTIONS = [
  { key: 1, labelKey: 'operations.range.hours_1', value: 1 },
  { key: 24, labelKey: 'operations.range.hours_24', value: 24 },
  { key: 168, labelKey: 'operations.range.hours_168', value: 168 },
  { key: 720, labelKey: 'operations.range.hours_720', value: 720 },
];

const CHANNEL_STATUS = {
  1: { labelKey: 'operations.status.enabled', color: 'green' },
  2: { labelKey: 'operations.status.manually_disabled', color: 'orange' },
  3: { labelKey: 'operations.status.auto_disabled', color: 'red' },
};

// 数字千分位格式跟随界面语言：英文界面用 1,234,567 之外的本地习惯（en-US），
// 中文界面保持 zh-CN，避免英文用户看到中文数字分组习惯。
const formatNumber = (value, locale) => {
  const num = Number(value || 0);
  return num.toLocaleString(locale);
};

const Operations = () => {
  const { t, i18n } = useTranslation();
  const locale = (i18n.language || 'zh').startsWith('en') ? 'en-US' : 'zh-CN';
  const [hours, setHours] = useState(24);
  const [loading, setLoading] = useState(false);
  const [summary, setSummary] = useState({
    quota: 0,
    request_count: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
  });
  const [modelStats, setModelStats] = useState([]);
  const [userStats, setUserStats] = useState([]);
  const [dailyStats, setDailyStats] = useState([]);
  const [channelHealth, setChannelHealth] = useState([]);

  const loadData = async (windowHours) => {
    setLoading(true);
    try {
      const [costRes, healthRes] = await Promise.all([
        API.get(`/api/log/cost?hours=${windowHours}&top=10`),
        API.get(`/api/channel/health?hours=${windowHours}`),
      ]);
      if (costRes.data.success) {
        const data = costRes.data.data || {};
        setSummary(data.summary || {});
        setModelStats(data.models || []);
        setUserStats(data.users || []);
        setDailyStats(data.daily || []);
      } else {
        showError(costRes.data.message);
      }
      if (healthRes.data.success) {
        setChannelHealth((healthRes.data.data || {}).items || []);
      } else {
        showError(healthRes.data.message);
      }
    } catch (error) {
      showError(error.message || t('operations.load_failed'));
    }
    setLoading(false);
  };

  useEffect(() => {
    loadData(hours);
  }, [hours]);

  // 日趋势图：额度换算为美元展示更直观
  const dailyChartData = dailyStats.map((item) => ({
    date: item.day,
    quota: Number(((item.quota || 0) / 500000).toFixed(4)),
    requests: item.request_count || 0,
  }));

  return (
    <div className='dashboard-container'>
      <Card fluid className='chart-card'>
        <Card.Content>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
            }}
          >
            <Header as='h3' style={{ margin: 0 }}>
              {t('operations.title')}
            </Header>
            <Dropdown
              selection
              options={HOUR_OPTIONS.map((option) => ({
                key: option.key,
                value: option.value,
                text: t(option.labelKey),
              }))}
              value={hours}
              onChange={(_, { value }) => setHours(value)}
            />
          </div>
        </Card.Content>
      </Card>

      <Segment loading={loading}>
        <Statistic.Group widths={4} size='small'>
          <Statistic>
            <Statistic.Value>{renderQuota(summary.quota || 0, t)}</Statistic.Value>
            <Statistic.Label>{t('operations.summary.total_quota')}</Statistic.Label>
          </Statistic>
          <Statistic>
            <Statistic.Value>
              {formatNumber(summary.request_count, locale)}
            </Statistic.Value>
            <Statistic.Label>{t('operations.summary.request_count')}</Statistic.Label>
          </Statistic>
          <Statistic>
            <Statistic.Value>
              {formatNumber(summary.prompt_tokens, locale)}
            </Statistic.Value>
            <Statistic.Label>{t('operations.summary.prompt_tokens')}</Statistic.Label>
          </Statistic>
          <Statistic>
            <Statistic.Value>
              {formatNumber(summary.completion_tokens, locale)}
            </Statistic.Value>
            <Statistic.Label>{t('operations.summary.completion_tokens')}</Statistic.Label>
          </Statistic>
        </Statistic.Group>
      </Segment>

      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header>{t('operations.trend.title')}</Card.Header>
          <div style={{ height: 260, marginTop: 12 }}>
            {dailyChartData.length === 0 ? (
              <Message info>{t('operations.trend.empty')}</Message>
            ) : (
              <ResponsiveContainer width='100%' height='100%'>
                <BarChart data={dailyChartData}>
                  <CartesianGrid strokeDasharray='3 3' vertical={false} opacity={0.1} />
                  <XAxis dataKey='date' axisLine={false} tickLine={false} tick={{ fontSize: 12, fill: '#A3AED0' }} />
                  <YAxis axisLine={false} tickLine={false} tick={{ fontSize: 12, fill: '#A3AED0' }} />
                  <Tooltip
                    contentStyle={{
                      background: '#fff',
                      border: 'none',
                      borderRadius: '4px',
                      boxShadow: '0 2px 8px rgba(0,0,0,0.1)',
                    }}
                  />
                  <Bar dataKey='quota' name={t('operations.trend.quota_series')} fill='#4318FF' radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            )}
          </div>
        </Card.Content>
      </Card>

      <Grid columns={2} stackable>
        <Grid.Column>
          <Card fluid className='chart-card'>
            <Card.Content>
              <Card.Header>{t('operations.models.title')}</Card.Header>
              <Table basic='very' compact unstackable>
                <Table.Header>
                  <Table.Row>
                    <Table.HeaderCell>{t('operations.table.model')}</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>{t('operations.table.requests')}</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>{t('operations.table.tokens')}</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>{t('operations.table.quota')}</Table.HeaderCell>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {modelStats.length === 0 ? (
                    <Table.Row>
                      <Table.Cell colSpan='4'>
                        <Message info>{t('operations.no_data')}</Message>
                      </Table.Cell>
                    </Table.Row>
                  ) : (
                    modelStats.map((item) => (
                      <Table.Row key={item.model_name}>
                        <Table.Cell>{item.model_name}</Table.Cell>
                        <Table.Cell textAlign='right'>
                          {formatNumber(item.request_count, locale)}
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {formatNumber(
                            (item.prompt_tokens || 0) + (item.completion_tokens || 0),
                            locale
                          )}
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {renderQuota(item.quota, t)}
                        </Table.Cell>
                      </Table.Row>
                    ))
                  )}
                </Table.Body>
              </Table>
            </Card.Content>
          </Card>
        </Grid.Column>

        <Grid.Column>
          <Card fluid className='chart-card'>
            <Card.Content>
              <Card.Header>{t('operations.users.title')}</Card.Header>
              <Table basic='very' compact unstackable>
                <Table.Header>
                  <Table.Row>
                    <Table.HeaderCell>{t('operations.table.user')}</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>{t('operations.table.requests')}</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>{t('operations.table.quota')}</Table.HeaderCell>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {userStats.length === 0 ? (
                    <Table.Row>
                      <Table.Cell colSpan='3'>
                        <Message info>{t('operations.no_data')}</Message>
                      </Table.Cell>
                    </Table.Row>
                  ) : (
                    userStats.map((item) => (
                      <Table.Row key={`${item.user_id}-${item.username}`}>
                        <Table.Cell>
                          {t('operations.user_label', {
                            name: item.username,
                            id: item.user_id,
                          })}
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {formatNumber(item.request_count, locale)}
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {renderQuota(item.quota, t)}
                        </Table.Cell>
                      </Table.Row>
                    ))
                  )}
                </Table.Body>
              </Table>
            </Card.Content>
          </Card>
        </Grid.Column>
      </Grid>

      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header>{t('operations.channels.title')}</Card.Header>
          <Table basic='very' compact unstackable>
            <Table.Header>
              <Table.Row>
                <Table.HeaderCell>{t('operations.table.channel')}</Table.HeaderCell>
                <Table.HeaderCell>{t('operations.table.status')}</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>{t('operations.table.requests')}</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>{t('operations.table.success_rate')}</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>{t('operations.table.avg_elapsed')}</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>{t('operations.table.quota')}</Table.HeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {channelHealth.length === 0 ? (
                <Table.Row>
                  <Table.Cell colSpan='6'>
                    <Message info>{t('operations.channels.empty')}</Message>
                  </Table.Cell>
                </Table.Row>
              ) : (
                channelHealth.map((item) => {
                  const status = CHANNEL_STATUS[item.status] || {
                    labelKey: 'operations.status.unknown',
                    color: 'grey',
                  };
                  const hasMetric = item.success_rate >= 0;
                  return (
                    <Table.Row key={item.channel_id}>
                      <Table.Cell>
                        {t('operations.channel_label', {
                          name: item.name,
                          id: item.channel_id,
                        })}
                      </Table.Cell>
                      <Table.Cell>
                        <Label color={status.color} size='small'>
                          {t(status.labelKey)}
                        </Label>
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {formatNumber(item.request_count, locale)}
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {hasMetric
                          ? `${(item.success_rate * 100).toFixed(1)}%`
                          : t('operations.no_metric_data')}
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {item.avg_elapsed_time > 0
                          ? `${(item.avg_elapsed_time / 1000).toFixed(2)}s`
                          : '-'}
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {renderQuota(item.quota, t)}
                      </Table.Cell>
                    </Table.Row>
                  );
                })
              )}
            </Table.Body>
          </Table>
          <Message size='small'>{t('operations.notice')}</Message>
        </Card.Content>
      </Card>
    </div>
  );
};

export default Operations;
