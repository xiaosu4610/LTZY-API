import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Form,
  Header,
  Message,
  Segment,
  Card,
} from 'semantic-ui-react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  API,
  copy,
  showError,
  showSuccess,
  timestamp2string,
} from '../../helpers';
import { renderQuotaWithPrompt } from '../../helpers/render';

// 可选的中继模式（与后端 relay/relaymode/name.go 的取值一一对应，C3）
// 后端校验会拒绝非法值，这里只做展示，避免前后端两处维护两套校验逻辑
//
// i18n 说明：表项里只存 labelKey（词条键）与 endpoint（接口路径），
// 显示文案在组件内拼接。原因是 endpoint 属于机器可读的协议常量，任何语言下都不该翻译，
// 把它和中文字面量混在一个 text 里会导致英文界面也必须显示中文模式名。
const ALLOWED_MODE_OPTIONS = [
  { key: 'chat', labelKey: 'token.edit.modes.chat', endpoint: '/v1/chat/completions', value: 'chat' },
  { key: 'completions', labelKey: 'token.edit.modes.completions', endpoint: '/v1/completions', value: 'completions' },
  { key: 'embeddings', labelKey: 'token.edit.modes.embeddings', endpoint: '/v1/embeddings', value: 'embeddings' },
  { key: 'moderations', labelKey: 'token.edit.modes.moderations', endpoint: '/v1/moderations', value: 'moderations' },
  { key: 'images', labelKey: 'token.edit.modes.images', endpoint: '/v1/images/generations', value: 'images' },
  { key: 'edits', labelKey: 'token.edit.modes.edits', endpoint: '/v1/edits', value: 'edits' },
  { key: 'audio_speech', labelKey: 'token.edit.modes.audio_speech', endpoint: '/v1/audio/speech', value: 'audio_speech' },
  {
    key: 'audio_transcription',
    labelKey: 'token.edit.modes.audio_transcription',
    endpoint: '/v1/audio/transcriptions',
    value: 'audio_transcription',
  },
  {
    key: 'audio_translation',
    labelKey: 'token.edit.modes.audio_translation',
    endpoint: '/v1/audio/translations',
    value: 'audio_translation',
  },
  { key: 'rerank', labelKey: 'token.edit.modes.rerank', endpoint: '/v1/rerank', value: 'rerank' },
  { key: 'responses', labelKey: 'token.edit.modes.responses', endpoint: '/v1/responses', value: 'responses' },
  { key: 'videos', labelKey: 'token.edit.modes.videos', endpoint: '/v1/videos', value: 'videos' },
  { key: 'proxy', labelKey: 'token.edit.modes.proxy', endpoint: '/v1/oneapi/proxy', value: 'proxy' },
  // A6 起这些端点已从 501 占位变为可用的透传中继，因此也纳入 scope 可选项
  { key: 'files', labelKey: 'token.edit.modes.files', endpoint: '/v1/files', value: 'files' },
  {
    key: 'fine_tuning',
    labelKey: 'token.edit.modes.fine_tuning',
    endpoint: '/v1/fine_tuning/jobs',
    value: 'fine_tuning',
  },
  { key: 'assistants', labelKey: 'token.edit.modes.assistants', endpoint: '/v1/assistants', value: 'assistants' },
  { key: 'threads', labelKey: 'token.edit.modes.threads', endpoint: '/v1/threads', value: 'threads' },
  { key: 'models', labelKey: 'token.edit.modes.models', endpoint: '/v1/models/{model}', value: 'models' },
  // A5 声音复刻/音色管理
  {
    key: 'audio_voice',
    labelKey: 'token.edit.modes.audio_voice',
    endpoint: '/v1/audio/voices',
    value: 'audio_voice',
  },
];

const EditToken = () => {
  const { t } = useTranslation();
  const params = useParams();
  const tokenId = params.id;
  const isEdit = tokenId !== undefined;
  const [loading, setLoading] = useState(isEdit);
  const [modelOptions, setModelOptions] = useState([]);
  const originInputs = {
    name: '',
    remain_quota: isEdit ? 0 : 500000,
    expired_time: -1,
    unlimited_quota: false,
    models: [],
    subnet: '',
    rpm: 0,
    tpm: 0,
    concurrency: 0,
    daily_quota_limit: 0,
    monthly_quota_limit: 0,
    allowed_modes: [],
  };
  const [inputs, setInputs] = useState(originInputs);
  const { name, remain_quota, expired_time, unlimited_quota } = inputs;
  const navigate = useNavigate();
  const handleInputChange = (e, { name, value }) => {
    setInputs((inputs) => ({ ...inputs, [name]: value }));
  };
  const handleCancel = () => {
    navigate('/token');
  };
  const setExpiredTime = (month, day, hour, minute) => {
    let now = new Date();
    let timestamp = now.getTime() / 1000;
    let seconds = month * 30 * 24 * 60 * 60;
    seconds += day * 24 * 60 * 60;
    seconds += hour * 60 * 60;
    seconds += minute * 60;
    if (seconds !== 0) {
      timestamp += seconds;
      setInputs({ ...inputs, expired_time: timestamp2string(timestamp) });
    } else {
      setInputs({ ...inputs, expired_time: -1 });
    }
  };

  const setUnlimitedQuota = () => {
    setInputs({ ...inputs, unlimited_quota: !unlimited_quota });
  };

  const loadToken = async () => {
    try {
      let res = await API.get(`/api/token/${tokenId}`);
      const { success, message, data } = res.data || {};
      if (success && data) {
        if (data.expired_time !== -1) {
          data.expired_time = timestamp2string(data.expired_time);
        }
        if (data.models === '') {
          data.models = [];
        } else {
          data.models = data.models.split(',');
        }
        // 接口 scope：字符串 ↔ 数组（LTZY-API 新增，C3）
        if (!data.allowed_modes) {
          data.allowed_modes = [];
        } else {
          data.allowed_modes = data.allowed_modes.split(',');
        }
        setInputs(data);
      } else {
        showError(message || t('token.edit.messages.load_failed'));
      }
    } catch (error) {
      showError(error.message || t('messages.error.network'));
    }
    setLoading(false);
  };

  const loadAvailableModels = async () => {
    try {
      let res = await API.get(`/api/user/available_models`);
      const { success, message, data } = res.data || {};
      if (success && data) {
        let options = data.map((model) => {
          return {
            key: model,
            text: model,
            value: model,
          };
        });
        setModelOptions(options);
      } else {
        showError(message || t('token.edit.messages.models_load_failed'));
      }
    } catch (error) {
      showError(error.message || t('messages.error.network'));
    }
  };

  useEffect(() => {
    if (isEdit) {
      loadToken().catch((error) => {
        showError(error.message || t('token.edit.messages.load_failed'));
        setLoading(false);
      });
    }
    loadAvailableModels().catch((error) => {
      showError(error.message || t('token.edit.messages.models_load_failed'));
    });
  }, []);

  const submit = async () => {
    if (!isEdit && inputs.name === '') return;
    let localInputs = inputs;
    localInputs.remain_quota = parseInt(localInputs.remain_quota);
    if (localInputs.expired_time !== -1) {
      let time = Date.parse(localInputs.expired_time);
      if (isNaN(time)) {
        showError(t('token.edit.messages.expire_time_invalid'));
        return;
      }
      localInputs.expired_time = Math.ceil(time / 1000);
    }
    localInputs.models = localInputs.models.join(',');
    // 接口 scope 是逗号分隔字符串，空数组表示不限制（LTZY-API 新增，C3）
    localInputs.allowed_modes = (localInputs.allowed_modes || []).join(',');
    let res;
    if (isEdit) {
      res = await API.put(`/api/token/`, {
        ...localInputs,
        id: parseInt(tokenId),
      });
    } else {
      res = await API.post(`/api/token/`, localInputs);
    }
    const { success, message } = res.data;
    if (success) {
      if (isEdit) {
        showSuccess(t('token.edit.messages.update_success'));
      } else {
        showSuccess(t('token.edit.messages.create_success'));
        setInputs(originInputs);
      }
    } else {
      showError(message);
    }
  };

  return (
    <div className='dashboard-container'>
      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header className='header'>
            {isEdit ? t('token.edit.title_edit') : t('token.edit.title_create')}
          </Card.Header>
          <Form loading={loading} autoComplete='new-password'>
            <Form.Field>
              <Form.Input
                label={t('token.edit.name')}
                name='name'
                placeholder={t('token.edit.name_placeholder')}
                onChange={handleInputChange}
                value={name}
                autoComplete='new-password'
                required={!isEdit}
              />
            </Form.Field>
            <Form.Field>
              <Form.Dropdown
                label={t('token.edit.models')}
                placeholder={t('token.edit.models_placeholder')}
                name='models'
                fluid
                multiple
                search
                onLabelClick={(e, { value }) => {
                  copy(value).then();
                }}
                selection
                onChange={handleInputChange}
                value={inputs.models}
                autoComplete='new-password'
                options={modelOptions}
              />
            </Form.Field>
            <Form.Field>
              <Form.Dropdown
                label={t('token.edit.allowed_modes')}
                placeholder={t('token.edit.allowed_modes_placeholder')}
                name='allowed_modes'
                fluid
                multiple
                selection
                onChange={handleInputChange}
                value={inputs.allowed_modes}
                autoComplete='new-password'
                options={ALLOWED_MODE_OPTIONS.map((option) => ({
                  key: option.key,
                  value: option.value,
                  // 模式名走词条，接口路径原样保留（协议常量，不翻译）
                  text: `${t(option.labelKey)} ${option.endpoint}`,
                }))}
              />
            </Form.Field>
            <Form.Field>
              <Form.Input
                label={t('token.edit.ip_limit')}
                name='subnet'
                placeholder={t('token.edit.ip_limit_placeholder')}
                onChange={handleInputChange}
                value={inputs.subnet}
                autoComplete='new-password'
              />
            </Form.Field>
            <Form.Field>
              <Form.Group widths='equal'>
                <Form.Input
                  label={t('token.edit.rpm')}
                  name='rpm'
                  placeholder={t('token.edit.rpm_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.rpm}
                  autoComplete='new-password'
                  type='number'
                  min='0'
                />
                <Form.Input
                  label={t('token.edit.tpm')}
                  name='tpm'
                  placeholder={t('token.edit.tpm_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.tpm}
                  autoComplete='new-password'
                  type='number'
                  min='0'
                />
                <Form.Input
                  label={t('token.edit.concurrency')}
                  name='concurrency'
                  placeholder={t('token.edit.concurrency_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.concurrency}
                  autoComplete='new-password'
                  type='number'
                  min='0'
                />
              </Form.Group>
            </Form.Field>
            <Form.Field>
              <Form.Group widths='equal'>
                <Form.Input
                  label={t('token.edit.daily_quota_limit')}
                  name='daily_quota_limit'
                  placeholder={t('token.edit.daily_quota_limit_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.daily_quota_limit}
                  autoComplete='new-password'
                  type='number'
                  min='0'
                />
                <Form.Input
                  label={t('token.edit.monthly_quota_limit')}
                  name='monthly_quota_limit'
                  placeholder={t('token.edit.monthly_quota_limit_placeholder')}
                  onChange={handleInputChange}
                  value={inputs.monthly_quota_limit}
                  autoComplete='new-password'
                  type='number'
                  min='0'
                />
              </Form.Group>
            </Form.Field>
            <Form.Field>
              <Form.Input
                label={t('token.edit.expire_time')}
                name='expired_time'
                placeholder={t('token.edit.expire_time_placeholder')}
                onChange={handleInputChange}
                value={expired_time}
                autoComplete='new-password'
                type='datetime-local'
              />
            </Form.Field>
            <div style={{ lineHeight: '40px' }}>
              <Button
                type={'button'}
                onClick={() => {
                  setExpiredTime(0, 0, 0, 0);
                }}
              >
                {t('token.edit.buttons.never_expire')}
              </Button>
              <Button
                type={'button'}
                onClick={() => {
                  setExpiredTime(1, 0, 0, 0);
                }}
              >
                {t('token.edit.buttons.expire_1_month')}
              </Button>
              <Button
                type={'button'}
                onClick={() => {
                  setExpiredTime(0, 1, 0, 0);
                }}
              >
                {t('token.edit.buttons.expire_1_day')}
              </Button>
              <Button
                type={'button'}
                onClick={() => {
                  setExpiredTime(0, 0, 1, 0);
                }}
              >
                {t('token.edit.buttons.expire_1_hour')}
              </Button>
              <Button
                type={'button'}
                onClick={() => {
                  setExpiredTime(0, 0, 0, 1);
                }}
              >
                {t('token.edit.buttons.expire_1_minute')}
              </Button>
            </div>
            <Message>{t('token.edit.quota_notice')}</Message>
            <Form.Field>
              <Form.Input
                label={`${t('token.edit.quota')}${renderQuotaWithPrompt(
                  remain_quota,
                  t
                )}`}
                name='remain_quota'
                placeholder={t('token.edit.quota_placeholder')}
                onChange={handleInputChange}
                value={remain_quota}
                autoComplete='new-password'
                type='number'
                disabled={unlimited_quota}
              />
            </Form.Field>
            <Button
              type={'button'}
              onClick={() => {
                setUnlimitedQuota();
              }}
            >
              {unlimited_quota
                ? t('token.edit.buttons.cancel_unlimited')
                : t('token.edit.buttons.unlimited_quota')}
            </Button>
            <Button floated='right' positive onClick={submit}>
              {t('token.edit.buttons.submit')}
            </Button>
            <Button floated='right' onClick={handleCancel}>
              {t('token.edit.buttons.cancel')}
            </Button>
          </Form>
        </Card.Content>
      </Card>
    </div>
  );
};

export default EditToken;
