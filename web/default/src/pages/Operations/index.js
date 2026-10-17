import React, { useEffect, useState } from 'react';
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

const HOUR_OPTIONS = [
  { key: 1, text: '最近 1 小时', value: 1 },
  { key: 24, text: '最近 24 小时', value: 24 },
  { key: 168, text: '最近 7 天', value: 168 },
  { key: 720, text: '最近 30 天', value: 720 },
];

const CHANNEL_STATUS = {
  1: { text: '已启用', color: 'green' },
  2: { text: '已手动禁用', color: 'orange' },
  3: { text: '已自动禁用', color: 'red' },
};

const formatNumber = (value) => {
  const num = Number(value || 0);
  return num.toLocaleString('zh-CN');
};

const Operations = () => {
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
      showError(error.message || '加载运营数据失败');
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
              运营看板
            </Header>
            <Dropdown
              selection
              options={HOUR_OPTIONS}
              value={hours}
              onChange={(_, { value }) => setHours(value)}
            />
          </div>
        </Card.Content>
      </Card>

      <Segment loading={loading}>
        <Statistic.Group widths={4} size='small'>
          <Statistic>
            <Statistic.Value>{renderQuota(summary.quota || 0)}</Statistic.Value>
            <Statistic.Label>总消耗额度</Statistic.Label>
          </Statistic>
          <Statistic>
            <Statistic.Value>
              {formatNumber(summary.request_count)}
            </Statistic.Value>
            <Statistic.Label>总请求数</Statistic.Label>
          </Statistic>
          <Statistic>
            <Statistic.Value>
              {formatNumber(summary.prompt_tokens)}
            </Statistic.Value>
            <Statistic.Label>输入 Token</Statistic.Label>
          </Statistic>
          <Statistic>
            <Statistic.Value>
              {formatNumber(summary.completion_tokens)}
            </Statistic.Value>
            <Statistic.Label>输出 Token</Statistic.Label>
          </Statistic>
        </Statistic.Group>
      </Segment>

      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header>每日消耗趋势（美元）</Card.Header>
          <div style={{ height: 260, marginTop: 12 }}>
            {dailyChartData.length === 0 ? (
              <Message info>该时间范围内暂无消费记录</Message>
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
                  <Bar dataKey='quota' name='消耗（$）' fill='#4318FF' radius={[4, 4, 0, 0]} />
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
              <Card.Header>模型消耗 Top 10</Card.Header>
              <Table basic='very' compact unstackable>
                <Table.Header>
                  <Table.Row>
                    <Table.HeaderCell>模型</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>请求数</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>Token</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>消耗</Table.HeaderCell>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {modelStats.length === 0 ? (
                    <Table.Row>
                      <Table.Cell colSpan='4'>
                        <Message info>暂无数据</Message>
                      </Table.Cell>
                    </Table.Row>
                  ) : (
                    modelStats.map((item) => (
                      <Table.Row key={item.model_name}>
                        <Table.Cell>{item.model_name}</Table.Cell>
                        <Table.Cell textAlign='right'>
                          {formatNumber(item.request_count)}
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {formatNumber(
                            (item.prompt_tokens || 0) + (item.completion_tokens || 0)
                          )}
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {renderQuota(item.quota)}
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
              <Card.Header>用户消耗 Top 10</Card.Header>
              <Table basic='very' compact unstackable>
                <Table.Header>
                  <Table.Row>
                    <Table.HeaderCell>用户</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>请求数</Table.HeaderCell>
                    <Table.HeaderCell textAlign='right'>消耗</Table.HeaderCell>
                  </Table.Row>
                </Table.Header>
                <Table.Body>
                  {userStats.length === 0 ? (
                    <Table.Row>
                      <Table.Cell colSpan='3'>
                        <Message info>暂无数据</Message>
                      </Table.Cell>
                    </Table.Row>
                  ) : (
                    userStats.map((item) => (
                      <Table.Row key={`${item.user_id}-${item.username}`}>
                        <Table.Cell>
                          {item.username}（#{item.user_id}）
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {formatNumber(item.request_count)}
                        </Table.Cell>
                        <Table.Cell textAlign='right'>
                          {renderQuota(item.quota)}
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
          <Card.Header>渠道健康度</Card.Header>
          <Table basic='very' compact unstackable>
            <Table.Header>
              <Table.Row>
                <Table.HeaderCell>渠道</Table.HeaderCell>
                <Table.HeaderCell>状态</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>请求数</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>成功率</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>平均耗时</Table.HeaderCell>
                <Table.HeaderCell textAlign='right'>消耗</Table.HeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {channelHealth.length === 0 ? (
                <Table.Row>
                  <Table.Cell colSpan='6'>
                    <Message info>暂无渠道数据</Message>
                  </Table.Cell>
                </Table.Row>
              ) : (
                channelHealth.map((item) => {
                  const status = CHANNEL_STATUS[item.status] || {
                    text: '未知',
                    color: 'grey',
                  };
                  const hasMetric = item.success_rate >= 0;
                  return (
                    <Table.Row key={item.channel_id}>
                      <Table.Cell>
                        {item.name}（#{item.channel_id}）
                      </Table.Cell>
                      <Table.Cell>
                        <Label color={status.color} size='small'>
                          {status.text}
                        </Label>
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {formatNumber(item.request_count)}
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {hasMetric
                          ? `${(item.success_rate * 100).toFixed(1)}%`
                          : '无数据'}
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {item.avg_elapsed_time > 0
                          ? `${(item.avg_elapsed_time / 1000).toFixed(2)}s`
                          : '-'}
                      </Table.Cell>
                      <Table.Cell textAlign='right'>
                        {renderQuota(item.quota)}
                      </Table.Cell>
                    </Table.Row>
                  );
                })
              )}
            </Table.Body>
          </Table>
          <Message size='small'>
            成功率来自实时监控采样，需要开启 `ENABLE_METRIC` 环境变量后才有数据；
            其余指标按时间窗口从消费日志统计。
          </Message>
        </Card.Content>
      </Card>
    </div>
  );
};

export default Operations;
