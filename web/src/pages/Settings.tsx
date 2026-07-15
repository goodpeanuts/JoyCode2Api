import React, { useEffect, useState } from 'react';
import {
  Card, Form, Input, Button, InputNumber, Select, Switch, message, Modal,
  Spin, Typography, Space, Row, Col, Tag, Tooltip,
} from 'antd';
import {
  SaveOutlined, ReloadOutlined, QuestionCircleOutlined,
  SettingOutlined, CheckCircleOutlined, InfoCircleOutlined, LockOutlined,
  SyncOutlined, CloudOutlined,
} from '@ant-design/icons';
import { api, authApi, clearToken } from '../api';
import type { Settings, ConfigRefreshStatus, Account, ModelInfo } from '../api';

const { Text } = Typography;

interface FieldConfig {
  key: string;
  label: string;
  tooltip: string;
  placeholder: string;
  type: 'input' | 'number' | 'select' | 'switch';
  options?: { label: string; value: string }[];
  suffix?: string;
  readOnly?: boolean;
  tag?: string;
}

const FIELD_GROUPS = [
  {
    title: '模型配置',
    fields: [
      {
        key: 'default_model',
        label: '默认模型',
        tag: '已生效',
        tooltip: '当客户端未指定模型，且账号未配置默认模型时使用的 JoyCode 模型',
        placeholder: 'JoyAI-Code',
        type: 'select' as const,
        options: [], // Will be populated dynamically
      },
      {
        key: 'default_max_tokens',
        label: '默认最大输出 Token',
        tooltip: '客户端未指定 max_tokens 时的默认值。更大值允许更长回复，但消耗更多配额',
        placeholder: '8192',
        type: 'number' as const,
        tag: '已生效',
      },
    ],
  },
  {
    title: '连接优化',
    fields: [
      {
        key: 'max_retries',
        label: '最大重试次数',
        tooltip: '请求失败时的自动重试次数。网络不稳定时可适当增加',
        placeholder: '3',
        type: 'number' as const,
        tag: '已生效',
      },
      {
        key: 'request_timeout',
        label: '请求超时（秒）',
        tooltip: '与 JoyCode 后端通信的读取超时时间，低于 60 秒会自动调整为 60 秒',
        placeholder: '120',
        type: 'number' as const,
        suffix: '秒',
        tag: '已生效',
      },
      {
        key: 'max_connections',
        label: '最大连接数',
        tooltip: '与 JoyCode 后端的最大并发 HTTP 连接数，修改后 10 秒内自动生效',
        placeholder: '20',
        type: 'number' as const,
        tag: '已生效',
      },
    ],
  },
  {
    title: '日志与监控',
    fields: [
      {
        key: 'enable_request_logging',
        label: '启用请求日志',
        tooltip: '记录每个 API 请求的详细信息（模型、延迟、状态码）。关闭后「数据概览」页面将无数据',
        placeholder: 'true',
        type: 'switch' as const,
        tag: '已生效',
      },
      {
        key: 'log_retention_days',
        label: '日志保留天数',
        tooltip: '请求日志的自动清理周期。超过此天数的日志将每小时自动清理，0 表示永久保留',
        placeholder: '30',
        type: 'number' as const,
        suffix: '天',
        tag: '已生效',
      },
      {
        key: 'timezone',
        label: '显示时区',
        tooltip: '日志时间与小时统计图按此时区显示。日志本身以 UTC 存储，切换时区只影响展示。首次打开会自动识别浏览器所在时区',
        placeholder: 'Asia/Shanghai',
        type: 'select' as const,
        tag: '已生效',
        options: [
          { label: 'Asia/Shanghai — 中国标准时间 (UTC+8)', value: 'Asia/Shanghai' },
          { label: 'Asia/Tokyo — 日本 (UTC+9)', value: 'Asia/Tokyo' },
          { label: 'Asia/Kolkata — 印度 (UTC+5:30)', value: 'Asia/Kolkata' },
          { label: 'UTC — 协调世界时', value: 'UTC' },
          { label: 'Europe/London — 英国', value: 'Europe/London' },
          { label: 'Europe/Paris — 中欧', value: 'Europe/Paris' },
          { label: 'America/New_York — 美东', value: 'America/New_York' },
          { label: 'America/Los_Angeles — 美西', value: 'America/Los_Angeles' },
        ],
      },
    ],
  },
  {
    title: '上游方言',
    fields: [
      {
        key: 'login_type',
        label: '登录类型 (loginType)',
        tooltip: '发送给上游的 loginType 请求头。凭据自带的值优先；此处设置作为无凭据时的默认值',
        placeholder: 'ERP',
        type: 'select' as const,
        options: [
          { label: 'ERP — 京东 ERP 登录（默认）', value: 'ERP' },
          { label: 'N_PIN_PC — JoyCoder 桌面 IDE', value: 'N_PIN_PC' },
          { label: 'PIN_JD_CLOUD — 京东云', value: 'PIN_JD_CLOUD' },
        ],
      },
      {
        key: 'source_type',
        label: '来源类型 (source-type)',
        tooltip: '发送给上游的 source-type 请求头，标识客户端类型',
        placeholder: 'joycoder-plugin',
        type: 'select' as const,
        options: [
          { label: 'joycoder-plugin — VS Code 插件（默认）', value: 'joycoder-plugin' },
          { label: 'joycoder-ide — JoyCoder 桌面 IDE', value: 'joycoder-ide' },
        ],
      },
      {
        key: 'client_name',
        label: '客户端标识 (client)',
        tooltip: '请求体中的 client 字段，标识客户端名称',
        placeholder: 'VS Code',
        type: 'input' as const,
      },
      {
        key: 'client_version',
        label: '客户端版本 (clientVersion)',
        tooltip: '请求体中的 clientVersion 字段',
        placeholder: '3.8.63',
        type: 'input' as const,
      },
      {
        key: 'user_agent',
        label: 'User-Agent',
        tooltip: '发送给上游的 User-Agent 请求头',
        placeholder: 'node',
        type: 'input' as const,
      },
      {
        key: 'tenant',
        label: '租户 (tenant)',
        tooltip: '请求体中的 tenant 字段。凭据自带的值优先；此处设置作为无凭据时的默认值',
        placeholder: 'JD',
        type: 'select' as const,
        options: [
          { label: 'JD — 京东（默认）', value: 'JD' },
          { label: 'JOYCODE — JoyCoder 通用', value: 'JOYCODE' },
        ],
      },
    ],
  },
];

const SettingsPage: React.FC = () => {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [changePwLoading, setChangePwLoading] = useState(false);
  const [configRefreshing, setConfigRefreshing] = useState(false);
  const [configStatus, setConfigStatus] = useState<ConfigRefreshStatus | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [refreshUserId, setRefreshUserId] = useState<string>('');
  const [dynamicModels, setDynamicModels] = useState<ModelInfo[]>([]);
  const [form] = Form.useForm();
  const [pwForm] = Form.useForm();

  const fetchSettings = async () => {
    setLoading(true);
    try {
      const data = await api.getSettings();
      // 后端设置统一存为字符串。switch 字段需转回布尔再回填表单，
      // 否则存着的 "false"（字符串）会被当成 truthy 而显示成「开」，
      // 误导用户。判定与后端一致：!= "false" 即视为开启。
      const switchKeys: string[] = [];
      for (const group of FIELD_GROUPS) {
        for (const field of group.fields) {
          if (field.type === 'switch') switchKeys.push(field.key);
        }
      }
      const normalized: Record<string, unknown> = { ...data };
      for (const key of switchKeys) {
        normalized[key] = data[key] !== 'false';
      }

      // First-run timezone auto-detect: if the backend has no timezone set,
      // seed it from the browser's IANA zone and persist it so log/stats times
      // render in the user's local timezone by default.
      if (!data.timezone) {
        const detected = Intl.DateTimeFormat().resolvedOptions().timeZone;
        if (detected) {
          normalized.timezone = detected;
          api.updateSettings({ timezone: detected }).catch(() => { /* best-effort seed */ });
        }
      }

      form.setFieldsValue(normalized);
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : '加载设置失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchSettings(); }, [form]);

  const fetchConfigStatus = async () => {
    try {
      const data = await api.configStatus();
      setConfigStatus(data.status);
    } catch {
      // Config refresher may not be available in older versions
    }
  };

  const fetchAccountsForRefresh = async () => {
    try {
      const accs = await api.listAccounts();
      setAccounts(accs);
      if (accs.length > 0) {
        const defaultAcc = accs.find(a => a.is_default);
        setRefreshUserId(defaultAcc ? defaultAcc.user_id : accs[0].user_id);
      }
    } catch {
      // ignore
    }
  };

  useEffect(() => { fetchConfigStatus(); fetchAccountsForRefresh(); }, []);

  const handleConfigRefresh = async () => {
    setConfigRefreshing(true);
    try {
      const result = await api.configRefresh(refreshUserId);
      if (result.ok) {
        message.success(`远端配置已刷新（${result.status.model_count} 个模型）`);
      } else {
        message.error(`刷新失败：${result.status.error || '未知错误'}`);
      }
      setConfigStatus(result.status);
      // Refresh dynamic model list after config refresh
      fetchDynamicModels();
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : '刷新远端配置失败');
    } finally {
      setConfigRefreshing(false);
    }
  };

  const fetchDynamicModels = async () => {
    try {
      const models = await api.listModels();
      setDynamicModels(models);
    } catch {
      // Use hardcoded fallback
    }
  };

  useEffect(() => { fetchDynamicModels(); }, []);

  const handleSave = async (values: Settings) => {
    setSaving(true);
    try {
      const payload = Object.fromEntries(
        Object.entries(values).map(([key, value]) => [key, value == null ? '' : String(value)])
      );
      await api.updateSettings(payload);
      message.success('设置已保存');
    } catch (e: unknown) {
      message.error(e instanceof Error ? e.message : '保存设置失败');
    } finally {
      setSaving(false);
    }
  };

  const handleChangePassword = async (values: { old_password: string; new_password: string }) => {
    Modal.confirm({
      title: '确认修改密码',
      content: '修改密码后需要重新登录，确定要继续吗？',
      okText: '确认修改',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: async () => {
        setChangePwLoading(true);
        try {
          await authApi.changePassword(values.old_password, values.new_password);
          message.success('密码修改成功，请重新登录');
          pwForm.resetFields();
          clearToken();
          setTimeout(() => { window.location.href = '/login'; }, 1000);
        } catch (e: unknown) {
          message.error(e instanceof Error ? e.message : '密码修改失败');
        } finally {
          setChangePwLoading(false);
        }
      },
    });
  };

  if (loading) return <Spin size="large" style={{ display: 'block', margin: '100px auto' }} />;

  const renderField = (field: FieldConfig) => {
    const label = (
      <Space size={4}>
        {field.label}
        <Tooltip title={field.tooltip}><QuestionCircleOutlined style={{ color: '#bbb' }} /></Tooltip>
        {field.tag && (
          <Tag color={field.tag === '已生效' ? 'success' : 'default'} style={{ marginLeft: 4, fontSize: 11 }}>
            {field.tag === '已生效' ? <CheckCircleOutlined /> : <InfoCircleOutlined />} {field.tag}
          </Tag>
        )}
      </Space>
    );

    switch (field.type) {
      case 'number':
        return (
          <Form.Item key={field.key} name={field.key} label={label}>
            <InputNumber
              style={{ width: '100%' }}
              placeholder={field.placeholder}
              addonAfter={field.suffix}
              disabled={field.readOnly}
            />
          </Form.Item>
        );
      case 'select': {
        // For default_model, use dynamic models with descriptions
        let selectOptions: Array<{ label: string; value: string; description?: string }> = field.options || [];
        if (field.key === 'default_model' && dynamicModels.length > 0) {
          selectOptions = dynamicModels.map(m => ({
            label: m.name || m.id,
            value: m.id,
            description: m.description,
          }));
        }
        // For timezone, make sure a browser-detected value not in the preset
        // list still shows up as a selectable option.
        if (field.key === 'timezone') {
          const cur = form.getFieldValue('timezone');
          if (cur && !selectOptions.some(o => o.value === cur)) {
            selectOptions = [{ label: cur, value: cur }, ...selectOptions];
          }
        }
        return (
          <Form.Item key={field.key} name={field.key} label={label}>
            <Select
              placeholder={field.placeholder}
              options={selectOptions}
              showSearch={field.key === 'timezone'}
              optionRender={(option) => {
                const desc = (option.data as { description?: string })?.description;
                return desc ? (
                  <Tooltip title={desc} placement="right" mouseEnterDelay={0.3}>
                    <div>
                      <div>{option.label}</div>
                      <div style={{ fontSize: 11, color: '#999', lineHeight: '14px' }}>{desc}</div>
                    </div>
                  </Tooltip>
                ) : <>{option.label}</>;
              }}
              allowClear
              disabled={field.readOnly}
            />
          </Form.Item>
        );
      }
      case 'switch':
        return (
          <Form.Item key={field.key} name={field.key} valuePropName="checked" label={label}>
            <Switch />
          </Form.Item>
        );
      default:
        return (
          <Form.Item key={field.key} name={field.key} label={label}>
            <Input placeholder={field.placeholder} disabled={field.readOnly} />
          </Form.Item>
        );
    }
  };

  return (
    <div>
      <Card
        style={{
          marginBottom: 16,
          background: 'linear-gradient(135deg, #00b578 0%, #009a63 100%)',
          border: 'none',
          borderRadius: 12,
        }}
        styles={{ body: { padding: '20px 24px' } }}
      >
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <Text style={{ color: 'rgba(255,255,255,0.85)', fontSize: 13 }}>
              JoyCode API 代理服务 · 系统设置
            </Text>
            <div style={{ color: '#fff', fontSize: 22, fontWeight: 700, marginTop: 4 }}>
              代理配置管理
            </div>
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <Button
              ghost
              style={{ color: '#fff', borderColor: 'rgba(255,255,255,0.4)' }}
              icon={<ReloadOutlined />}
              onClick={fetchSettings}
            >
              刷新
            </Button>
          </div>
        </div>
      </Card>

      <Form form={form} layout="vertical" onFinish={handleSave}>
        {FIELD_GROUPS.map((group) => (
          <Card
            key={group.title}
            title={<Text strong style={{ fontSize: 15 }}>{group.title}</Text>}
            style={{ marginBottom: 16, borderRadius: 8, border: '1px solid #f0f0f0' }}
            styles={{ body: { padding: '20px 24px' } }}
            extra={
              <SettingOutlined style={{ color: '#00b578' }} />
            }
          >
            <Row gutter={[24, 0]}>
              {group.fields.map((field) => (
                <Col xs={24} md={12} key={field.key}>
                  {renderField(field)}
                </Col>
              ))}
            </Row>
          </Card>
        ))}

        <Card
          title={
            <Space>
              <CloudOutlined />
              <Text strong style={{ fontSize: 15 }}>远端配置刷新</Text>
            </Space>
          }
          style={{ marginBottom: 16, borderRadius: 8, border: '1px solid #f0f0f0' }}
          styles={{ body: { padding: '20px 24px' } }}
          extra={<SettingOutlined style={{ color: '#00b578' }} />}
        >
          <div style={{ marginBottom: 16 }}>
            <Text type="secondary" style={{ fontSize: 13 }}>
              从 JoyCode 服务端获取最新的模型列表和插件配置，用于动态更新支持的模型和配置信息。
              后台每 30 分钟自动刷新一次，也可手动触发。
            </Text>
          </div>
          <Row gutter={[24, 16]} align="middle">
            {accounts.length > 0 && (
              <Col xs={24} md={8}>
                <div style={{ marginBottom: 4 }}>
                  <Text strong style={{ fontSize: 13 }}>使用凭据</Text>
                  <Tooltip title="选择使用哪个账号的凭据来获取远端配置，默认使用第一个凭据">
                    <QuestionCircleOutlined style={{ color: '#bbb', marginLeft: 4 }} />
                  </Tooltip>
                </div>
                <Select
                  style={{ width: '100%' }}
                  value={refreshUserId}
                  onChange={setRefreshUserId}
                  options={accounts.map(a => ({
                    label: `${a.remark || a.nickname || a.user_id}${a.is_default ? ' (默认)' : ''}`,
                    value: a.user_id,
                  }))}
                />
              </Col>
            )}
            <Col xs={24} md={accounts.length > 0 ? 8 : 12}>
              <Button
                type="primary"
                icon={<SyncOutlined spin={configRefreshing} />}
                loading={configRefreshing}
                onClick={handleConfigRefresh}
                style={{ borderRadius: 6 }}
              >
                立即刷新配置
              </Button>
            </Col>
            <Col xs={24} md={accounts.length > 0 ? 8 : 12}>
              {configStatus && (
                <Space direction="vertical" size={2}>
                  <Space size={4}>
                    <Tag color={configStatus.success ? 'success' : 'error'}>
                      {configStatus.success ? '✓ 上次刷新成功' : '✗ 上次刷新失败'}
                    </Tag>
                  </Space>
                  <Text type="secondary" style={{ fontSize: 12 }}>
                    模型数量: {configStatus.model_count}
                    {configStatus.last_refresh && ` · 刷新时间: ${new Date(configStatus.last_refresh).toLocaleString()}`}
                    {configStatus.duration && ` · 耗时: ${configStatus.duration}`}
                    {configStatus.refreshed_by && ` · 凭据: ${configStatus.refreshed_by}`}
                  </Text>
                  {configStatus.error && (
                    <Text type="danger" style={{ fontSize: 12 }}>错误: {configStatus.error}</Text>
                  )}
                </Space>
              )}
            </Col>
          </Row>
        </Card>

        <Card
          title={<Text strong style={{ fontSize: 15 }}>安全设置</Text>}
          style={{ marginBottom: 16, borderRadius: 8, border: '1px solid #f0f0f0' }}
          styles={{ body: { padding: '20px 24px' } }}
          extra={<SettingOutlined style={{ color: '#00b578' }} />}
        >
          <Form form={pwForm} layout="vertical" onFinish={handleChangePassword}>
            <Row gutter={[24, 0]}>
              <Col xs={24} md={8}>
                <Form.Item name="old_password" label="当前密码" rules={[{ required: true, message: '请输入当前密码' }]}>
                  <Input.Password placeholder="输入当前密码" />
                </Form.Item>
              </Col>
              <Col xs={24} md={8}>
                <Form.Item name="new_password" label="新密码" rules={[
                  { required: true, message: '请输入新密码' },
                  { min: 6, message: '密码长度不能少于 6 位' },
                ]}>
                  <Input.Password placeholder="输入新密码（至少 6 位）" />
                </Form.Item>
              </Col>
              <Col xs={24} md={8}>
                <Form.Item label="确认新密码" dependencies={['new_password']} rules={[
                  { required: true, message: '请确认新密码' },
                  ({ getFieldValue }) => ({
                    validator(_, value) {
                      if (!value || getFieldValue('new_password') === value) {
                        return Promise.resolve();
                      }
                      return Promise.reject(new Error('两次输入的密码不一致'));
                    },
                  }),
                ]} name="confirm_password">
                  <Input.Password placeholder="再次输入新密码" />
                </Form.Item>
              </Col>
            </Row>
            <Button
              type="primary"
              htmlType="submit"
              loading={changePwLoading}
              icon={<LockOutlined />}
              style={{ borderRadius: 6 }}
            >
              修改密码
            </Button>
          </Form>
        </Card>

        <div style={{ display: 'flex', gap: 12, marginTop: 8 }}>
          <Button
            type="primary"
            htmlType="submit"
            loading={saving}
            icon={<SaveOutlined />}
            size="large"
            style={{ borderRadius: 6 }}
          >
            保存设置
          </Button>
          <Button onClick={fetchSettings} icon={<ReloadOutlined />} size="large">
            恢复当前值
          </Button>
        </div>
      </Form>
    </div>
  );
};

export default SettingsPage;
