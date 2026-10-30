import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Form, Input, InputNumber, message, Spin, Switch, Tabs } from 'antd';
import { useAtom } from 'jotai';
import { ChevronDownIcon, DownloadIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  configureBmc,
  getBmcStatus,
  ipmiPackageAction,
  type BmcSettings,
  type BmcStatus
} from '@/api/bmc.ts';
import { localized } from '@/lib/addon-form.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { bmcTabAtom } from '@/jotai/settings.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

import { AddonPage } from '../addon';
import { useAddonSections } from '../addon/sections';

// Services > Redfish and IPMI: Redfish is part of the server, IPMI comes with
// its add-on; one tab each, and below them the BMC account for both, with the
// power options and the IPMI port under Advanced. The two switches apply at
// once; Save is for the account. Other add-ons that extend this page
// (settings.section "services-bmc") get a tab of their own.
export const BmcSettingsPage = () => {
  const { t, i18n } = useTranslation('translation', { keyPrefix: 'settings.bmc' });
  const sections = useAddonSections('services-bmc');
  const [activeTab, setActiveTab] = useAtom(bmcTabAtom);
  const [status, setStatus] = useState<BmcStatus>();
  // Reading the status and the last switch, save or install fail apart:
  // the 5 s poll must not hide why a change was refused.
  const [loadError, setLoadError] = useState('');
  const [actionError, setActionError] = useState('');
  const [busy, setBusy] = useState(false);
  const [switching, setSwitching] = useState<'redfish' | 'ipmi'>();
  const [installing, setInstalling] = useState(false);
  const [showValidationErrors, setShowValidationErrors] = useState(false);
  const [form] = Form.useForm<BmcSettings>();
  const dirty = useRef(false);
  const mounted = useRef(false);
  const operation = useRef(false);
  const edits = useRef(0);
  const generation = useRef(0);
  const reading = useRef(false);
  const stored = useRef<BmcStatus | undefined>(undefined);
  const invalidateReads = useCallback(() => {
    generation.current++;
    reading.current = false;
  }, []);
  const ipmi = Form.useWatch('ipmi', form);
  const redfish = Form.useWatch('redfish', form);

  const show = useCallback(
    (next: BmcStatus) => {
      stored.current = next;
      setStatus(next);
      if (!dirty.current) {
        form.setFieldsValue({ ...next, holdSeconds: next.holdSeconds || undefined, password: '' });
      } else if (next.passwordSet) {
        // Account edits stay local; switches still track the applied settings.
        form.setFieldsValue({ redfish: next.redfish, ipmi: next.ipmi });
      }
    },
    [form]
  );

  const refresh = useCallback(
    async (afterMutation = false) => {
      if (!mounted.current || reading.current || (operation.current && !afterMutation)) return;
      const request = ++generation.current;
      reading.current = true;
      try {
        const response = await getBmcStatus();
        if (!mounted.current || request !== generation.current) return;
        if (response.code !== 0) throw new Error(response.msg);
        show(response.data);
        setLoadError('');
      } catch {
        if (mounted.current && request === generation.current) setLoadError(t('failed'));
      } finally {
        if (request === generation.current) reading.current = false;
      }
    },
    [show, t]
  );

  const begin = () => {
    if (operation.current) return false;
    operation.current = true;
    // A read begun before this write must never restore the previous config.
    invalidateReads();
    return true;
  };

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 5000);
    return () => {
      mounted.current = false;
      invalidateReads();
      stop();
    };
  }, [refresh, invalidateReads]);

  const save = async (values: BmcSettings) => {
    const previous = stored.current;
    if (!previous || !begin()) return;
    const submittedEdit = edits.current;
    const settings = {
      ...values,
      ipmi: Boolean(values.ipmi),
      // The port field exists only with the add-on installed.
      ipmiPort: values.ipmiPort ?? previous.ipmiPort,
      holdSeconds: values.holdSeconds || 0,
      powerLedConnected: values.powerLedConnected !== false,
      password: values.password ?? ''
    };
    setBusy(true);
    try {
      const response = await configureBmc(settings);
      if (!mounted.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      if (edits.current === submittedEdit) dirty.current = false;
      if (form.getFieldValue('password') === values.password) form.setFieldValue('password', '');
      const { password, ...applied } = settings;
      show({ ...previous, ...applied, passwordSet: previous.passwordSet || !!password });
      setShowValidationErrors(false);
      void message.success(t('saved'));
      setActionError('');
    } catch (err) {
      if (mounted.current) setActionError(err instanceof Error ? err.message : t('failed'));
    } finally {
      await refresh(true);
      operation.current = false;
      if (mounted.current) setBusy(false);
    }
  };

  // A switch applies at once with the stored account, not with fields being
  // edited. Turning one on needs a stored password: until the account is
  // saved the switch stays on the page and Save turns it on.
  const toggle = async (key: 'redfish' | 'ipmi', on: boolean) => {
    const previous = stored.current;
    if (!previous) return;
    if (operation.current) {
      form.setFieldValue(key, previous[key]);
      return;
    }
    if (!previous.passwordSet || on === previous[key]) {
      dirty.current = true;
      edits.current++;
      return;
    }
    if (!begin()) return;
    setSwitching(key);
    try {
      const response = await configureBmc({
        redfish: previous.redfish,
        ipmi: previous.ipmi,
        username: previous.username,
        password: '',
        ipmiPort: previous.ipmiPort,
        holdSeconds: previous.holdSeconds,
        powerLedConnected: previous.powerLedConnected,
        [key]: on
      });
      if (!mounted.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      show({ ...previous, [key]: on });
      setActionError('');
    } catch (err) {
      if (mounted.current) {
        form.setFieldValue(key, previous[key]);
        setActionError(err instanceof Error ? err.message : t('failed'));
      }
    } finally {
      await refresh(true);
      operation.current = false;
      if (mounted.current) setSwitching(undefined);
    }
  };

  const installIpmi = async () => {
    if (!begin()) return;
    setInstalling(true);
    try {
      const response = await ipmiPackageAction('install');
      if (!mounted.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      setActionError('');
    } catch (err) {
      if (mounted.current) setActionError(err instanceof Error ? err.message : t('failed'));
    } finally {
      await refresh(true);
      operation.current = false;
      if (mounted.current) setInstalling(false);
    }
  };

  // The page has no <form> element (see below): Enter submits by hand.
  const submit = () => form.submit();

  if (!status) return loadError ? <Alert type="error" title={loadError} showIcon /> : <Spin />;
  // An add-on removed while its tab is open: back to Redfish, not an empty page.
  const currentTab =
    activeTab.startsWith('addon-') &&
    !sections.some((manifest) => `addon-${manifest.id}` === activeTab)
      ? 'redfish'
      : activeTab;
  // The account and the power options serve Redfish and IPMI: without
  // either they configure nothing. Hidden, not unmounted, so a save keeps them.
  const shared = Boolean(redfish) || status.ipmiInstalled;
  // A switch turned on before there is a password: it waits for Save.
  const waiting =
    !status.passwordSet &&
    ((Boolean(redfish) && !status.redfish) || (Boolean(ipmi) && !status.ipmi));

  return (
    <div className="space-y-6">
      <p className="text-fg-muted m-0 text-sm">{t('description')}</p>
      {loadError && <Alert type="error" title={loadError} showIcon />}
      {actionError && <Alert type="error" title={actionError} showIcon />}
      {/* No <form> element: an add-on section in a tab may bring its own form. */}
      <Form
        form={form}
        component={false}
        layout="vertical"
        onValuesChange={(changed: Partial<BmcSettings>) => {
          // The switches apply on their own (toggle); the poll may update them.
          if (Object.keys(changed).some((key) => key !== 'redfish' && key !== 'ipmi')) {
            dirty.current = true;
            edits.current++;
          }
        }}
        onFinish={(values) => void save(values)}
        onFinishFailed={() => setShowValidationErrors(true)}
      >
        <Tabs
          activeKey={currentTab}
          onChange={setActiveTab}
          items={[
            {
              key: 'redfish',
              label: t('redfish'),
              // Rendered while hidden: a save sends the fields of every tab.
              forceRender: true,
              children: (
                <SettingRow
                  label={t('redfishEnable')}
                  description={t('redfishHint', { host: window.location.host })}
                  htmlFor="bmc-redfish"
                >
                  <Form.Item name="redfish" valuePropName="checked" noStyle>
                    <Switch
                      id="bmc-redfish"
                      aria-describedby="bmc-redfish-description"
                      loading={switching === 'redfish'}
                      disabled={busy || !!switching || installing}
                      onChange={(on) => void toggle('redfish', on)}
                    />
                  </Form.Item>
                </SettingRow>
              )
            },
            {
              key: 'ipmi',
              label: t('ipmi'),
              forceRender: true,
              children: (
                <div className="space-y-4">
                  {status.ipmiInstalled ? (
                    <>
                      <StatusBadge tone={status.ipmiRunning ? 'success' : 'neutral'}>
                        {status.ipmiRunning ? t('running') : t('stopped')}
                      </StatusBadge>
                      <SettingRow
                        label={t('ipmiEnable')}
                        description={t('ipmiHint')}
                        htmlFor="bmc-ipmi"
                      >
                        <Form.Item name="ipmi" valuePropName="checked" noStyle>
                          <Switch
                            id="bmc-ipmi"
                            aria-describedby="bmc-ipmi-description"
                            loading={switching === 'ipmi'}
                            disabled={busy || !!switching || installing}
                            onChange={(on) => void toggle('ipmi', on)}
                          />
                        </Form.Item>
                      </SettingRow>
                      {ipmi && <Alert type="warning" title={t('ipmiWarning')} showIcon />}
                    </>
                  ) : (
                    <>
                      <p className="text-fg-muted m-0 text-sm">{t('ipmiNotInstalled')}</p>
                      <Button
                        icon={<DownloadIcon size={16} />}
                        loading={installing}
                        disabled={busy || !!switching || installing}
                        onClick={() => void installIpmi()}
                      >
                        {t('ipmiInstall')}
                      </Button>
                    </>
                  )}
                  {/* Its status: the badge above already says when it is stopped. */}
                  {status.ipmiRunning &&
                    sections
                      .filter((manifest) => manifest.id === 'ipmi')
                      .map((manifest) => (
                        <AddonPage key={manifest.id} manifest={manifest} embedded />
                      ))}
                </div>
              )
            },
            ...sections
              .filter((manifest) => manifest.id !== 'ipmi')
              .map((manifest) => ({
                key: `addon-${manifest.id}`,
                label: localized(manifest.title, i18n.language),
                // The tab carries its title: drawn as a page of its own.
                children: <AddonPage manifest={manifest} />
              }))
          ]}
        />
        {/* The account and Save belong to Redfish and IPMI, not to an add-on's tab. */}
        <div className="space-y-6" hidden={currentTab.startsWith('addon-')}>
          {waiting && <Alert type="info" title={t('accountFirst')} showIcon />}
          {!shared && !showValidationErrors && (
            <p className="text-fg-muted m-0 text-sm">{t('sharedOff')}</p>
          )}
          {/* A rejected draft must stay editable even after Redfish is switched off. */}
          <div hidden={!shared && !showValidationErrors}>
            <SettingsSection
              title={t('account')}
              description={status.ipmiInstalled ? t('accountHint') : t('accountHintRedfish')}
            >
              <Form.Item
                name="username"
                label={t('username')}
                rules={[
                  { required: true, pattern: /^[A-Za-z0-9._-]{1,16}$/, message: t('usernameRule') }
                ]}
              >
                <Input autoComplete="off" maxLength={16} onPressEnter={submit} />
              </Form.Item>
              <Form.Item
                name="password"
                label={t('password')}
                extra={status.passwordSet ? t('passwordKeep') : t('passwordRule')}
                rules={[
                  { min: 8, message: t('passwordRule') },
                  { max: 20, message: t('passwordRule') }
                ]}
              >
                <Input.Password autoComplete="new-password" maxLength={20} onPressEnter={submit} />
              </Form.Item>
              {/* Rendered while folded: a save sends these fields too. */}
              <Panel flush className="overflow-hidden">
                <details className="group">
                  <summary className="text-fg-muted hover:text-fg flex cursor-pointer list-none items-center justify-between gap-3 px-4 py-3 text-sm [&::-webkit-details-marker]:hidden">
                    <span>{t('advanced')}</span>
                    <ChevronDownIcon
                      size={16}
                      className="transition-transform group-open:rotate-180"
                    />
                  </summary>
                  <div className="px-4 pb-1">
                    <Form.Item name="holdSeconds" label={t('holdSeconds')} extra={t('holdHint')}>
                      <InputNumber
                        min={1}
                        max={30}
                        placeholder={t('holdDefault')}
                        onPressEnter={submit}
                      />
                    </Form.Item>
                    <Form.Item
                      name="powerLedConnected"
                      label={t('powerLed')}
                      valuePropName="checked"
                      extra={t('powerLedHint')}
                    >
                      <Switch />
                    </Form.Item>
                    {status.ipmiInstalled && (
                      <Form.Item name="ipmiPort" label={t('ipmiPort')}>
                        <InputNumber min={1} max={65535} onPressEnter={submit} />
                      </Form.Item>
                    )}
                  </div>
                </details>
              </Panel>
              <Button
                type="primary"
                loading={busy}
                disabled={busy || !!switching || installing}
                onClick={submit}
              >
                {t('save')}
              </Button>
            </SettingsSection>
          </div>
        </div>
      </Form>
    </div>
  );
};
