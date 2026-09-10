import { useEffect, useState } from 'react';
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons';
import {
  Button,
  InputNumber,
  message,
  Modal,
  Popover,
  Segmented,
  Select,
  Space,
  Tooltip,
  Typography
} from 'antd';
import { useAtom, useSetAtom } from 'jotai';
import { ScanSearchIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  getInputRegion,
  setManualRegions as saveManualRegions,
  setControlRegionMode,
  setOriginalResolutionConfig,
  setSelectedOriginalResolution
} from '@/api/vm.ts';
import {
  ControlRegionConfig,
  ControlRegionMode,
  InputRegion,
  OriginalResolution as ResolutionPreset
} from '@/types';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import {
  controlRegionModeAtom,
  inputRegionAtom,
  inputRegionSelectingAtom,
  manualInputRegionAtom,
  manualRegionsAtom,
  selectedManualRegionAtom,
  selectedOriginalResolutionAtom
} from '@/jotai/screen.ts';
import { menuCloseSignalAtom } from '@/jotai/settings.ts';
import { isValidInputRegion } from '@/pages/desktop/screen/geometry.ts';

const resolutionKey = ({ width, height }: ResolutionPreset) => `${width}x${height}`;

export const OriginalResolution = () => {
  const { t } = useTranslation();
  const setInputRegion = useSetAtom(inputRegionAtom);
  const setManualInputRegion = useSetAtom(manualInputRegionAtom);
  const [manualRegions, setManualRegions] = useAtom(manualRegionsAtom);
  const [selectedManualRegion, setSelectedManualRegion] = useAtom(selectedManualRegionAtom);
  const [selectedOriginalResolution, setSelectedOriginalResolutionState] = useAtom(
    selectedOriginalResolutionAtom
  );
  const [mode, setMode] = useAtom(controlRegionModeAtom);
  const setSelecting = useSetAtom(inputRegionSelectingAtom);
  const setKeyboardLock = useSetAtom(keyboardLockAtom);
  const requestMenuClose = useSetAtom(menuCloseSignalAtom);
  const [messageApi, contextHolder] = message.useMessage();
  const [isPopoverOpen, setIsPopoverOpen] = useState(false);
  const [isAddOpen, setIsAddOpen] = useState(false);
  const [resolutions, setResolutions] = useState<ResolutionPreset[]>([]);
  const [newWidth, setNewWidth] = useState<number | null>(null);
  const [newHeight, setNewHeight] = useState<number | null>(null);

  useEffect(() => {
    return () => {
      setKeyboardLock({ source: 'control-region-popover', locked: false });
      setKeyboardLock({ source: 'control-region-resolution-modal', locked: false });
    };
  }, [setKeyboardLock]);

  useEffect(() => {
    setKeyboardLock({ source: 'control-region-resolution-modal', locked: isAddOpen });
  }, [isAddOpen, setKeyboardLock]);

  async function loadConfig() {
    const rsp = await getInputRegion();
    if (rsp.code === 0) {
      const config = rsp.data as ControlRegionConfig;
      setResolutions(config?.resolutions || []);
      setManualRegions(config?.regions || []);
      setSelectedManualRegion(config?.selectedRegion || '');
      setSelectedOriginalResolutionState(config?.selectedResolution || '');
      setManualInputRegion(
        isValidInputRegion(config as InputRegion) ? (config as InputRegion) : null
      );
    }
    return rsp;
  }

  async function applyOriginalResolution(key: string) {
    const rsp = await setSelectedOriginalResolution(key);
    if (rsp.code !== 0) {
      messageApi.error(t('screen.controlRegion.saveFailed'));
      return;
    }
    setSelectedOriginalResolutionState(key);
    setSelectedManualRegion('');
    if (!key) setInputRegion(null);
    setIsPopoverOpen(true);
  }

  async function applyManualRegion(key: string) {
    const rsp = await saveManualRegions(manualRegions, key);
    if (rsp.code !== 0) {
      messageApi.error(t('screen.controlRegion.saveFailed'));
      return;
    }
    setSelectedManualRegion(key);
    setSelectedOriginalResolutionState('');
    setInputRegion(
      manualRegions.find((region) => `${region.width}x${region.height}` === key) || null
    );
    setIsPopoverOpen(true);
  }

  async function saveResolutions(
    next: ResolutionPreset[],
    nextSelected = selectedOriginalResolution
  ) {
    const rsp = await setOriginalResolutionConfig(
      next,
      nextSelected,
      nextSelected ? '' : selectedManualRegion
    );
    if (rsp.code !== 0) {
      messageApi.error(t('screen.controlRegion.saveFailed'));
      return false;
    }
    setResolutions(next);
    return true;
  }

  async function addResolution() {
    if (!newWidth || !newHeight) {
      messageApi.error(t('screen.controlRegion.invalidResolution'));
      return;
    }
    const resolution = { width: newWidth, height: newHeight };
    if (resolutions.some((item) => resolutionKey(item) === resolutionKey(resolution))) {
      messageApi.warning(t('screen.controlRegion.duplicateResolution'));
      return;
    }
    if (await saveResolutions([...resolutions, resolution])) {
      setNewWidth(null);
      setNewHeight(null);
      setIsAddOpen(false);
    }
  }

  async function deleteResolution(index: number) {
    if (index < 0 || index >= resolutions.length) return;
    const deleted = resolutionKey(resolutions[index]);
    const nextSelected = selectedOriginalResolution === deleted ? '' : selectedOriginalResolution;
    if (
      await saveResolutions(
        resolutions.filter((_, itemIndex) => itemIndex !== index),
        nextSelected
      )
    ) {
      if (!nextSelected) {
        setSelectedOriginalResolutionState('');
        setInputRegion(null);
      }
    }
  }

  async function deleteManualRegion(index: number) {
    if (index < 0 || index >= manualRegions.length) return;
    const deleted = `${manualRegions[index].width}x${manualRegions[index].height}`;
    const nextSelected = selectedManualRegion === deleted ? '' : selectedManualRegion;
    const next = manualRegions.filter((_, itemIndex) => itemIndex !== index);
    const rsp = await saveManualRegions(next, nextSelected, selectedOriginalResolution);
    if (rsp.code !== 0) {
      messageApi.error(t('screen.controlRegion.saveFailed'));
      return;
    }
    setManualRegions(next);
    if (!nextSelected) {
      setSelectedManualRegion('');
      setInputRegion(null);
    }
  }

  async function updateMode(nextMode: ControlRegionMode) {
    const rsp = await setControlRegionMode(nextMode);
    if (rsp.code !== 0) {
      messageApi.error(t('screen.controlRegion.saveFailed'));
      return;
    }
    setMode(nextMode);
    if (nextMode === 'manual') {
      const config = await loadConfig();
      const manualRegion = isValidInputRegion(config.data as InputRegion)
        ? (config.data as InputRegion)
        : null;
      const selectedRegion = (config.data as ControlRegionConfig)?.selectedRegion || '';
      const selectedResolution = (config.data as ControlRegionConfig)?.selectedResolution || '';
      const regions = (config.data as ControlRegionConfig)?.regions || [];
      setManualInputRegion(manualRegion);
      setInputRegion(
        selectedResolution
          ? null
          : regions.find((region) => `${region.width}x${region.height}` === selectedRegion) || null
      );
    } else {
      setInputRegion(null);
    }
  }

  function selectArea() {
    handleOpenChange(false);
    requestMenuClose((signal) => signal + 1);
    setSelecting(true);
  }

  function handleOpenChange(open: boolean) {
    setIsPopoverOpen(open);
    setKeyboardLock({ source: 'control-region-popover', locked: open });
    if (open) loadConfig();
  }

  const content = (
    <div className="w-[250px]">
      <Space direction="vertical" size="small" className="mt-2 w-full">
        <Segmented<ControlRegionMode>
          block
          value={mode}
          options={[
            { label: t('screen.controlRegion.off'), value: 'off' },
            { label: t('screen.controlRegion.auto'), value: 'auto' },
            { label: t('screen.controlRegion.manual'), value: 'manual' }
          ]}
          onChange={updateMode}
        />
        <Typography.Text type="secondary" className="text-xs">
          {t('screen.controlRegion.description')}
        </Typography.Text>
        {mode === 'auto' && (
          <Typography.Text type="warning" className="text-xs">
            {t('screen.controlRegion.autoWarning')}
          </Typography.Text>
        )}
        {mode === 'manual' && (
          <>
            <Button block type="primary" icon={<ScanSearchIcon size={14} />} onClick={selectArea}>
              {t('screen.controlRegion.select')}
            </Button>
            <Typography.Text>{t('screen.controlRegion.selectedResolution')}</Typography.Text>
            <Select
              className="w-full"
              value={selectedManualRegion}
              options={[
                { label: t('screen.controlRegion.unused'), value: '' },
                ...manualRegions.map((region) => ({
                  label: `${region.width}x${region.height}`,
                  value: `${region.width}x${region.height}`
                }))
              ]}
              optionRender={(option) => (
                <div className="flex items-center justify-between">
                  <span>{option.label}</span>
                  {option.value !== '' && (
                    <Button
                      type="text"
                      danger
                      size="small"
                      icon={<DeleteOutlined />}
                      onMouseDown={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                      }}
                      onClick={(event) => {
                        event.stopPropagation();
                        deleteManualRegion(
                          manualRegions.findIndex(
                            (region) => `${region.width}x${region.height}` === option.value
                          )
                        );
                      }}
                    />
                  )}
                </div>
              )}
              onChange={applyManualRegion}
            />
            <Typography.Text>{t('screen.controlRegion.originalResolution')}</Typography.Text>
            <Space.Compact block>
              <Select
                className="w-full"
                value={selectedOriginalResolution}
                placeholder={t('screen.controlRegion.selectResolution')}
                getPopupContainer={(trigger) => trigger.parentElement || document.body}
                options={[
                  { label: t('screen.controlRegion.unused'), value: '' },
                  ...resolutions.map((resolution) => ({
                    label: resolutionKey(resolution),
                    value: resolutionKey(resolution)
                  }))
                ]}
                optionRender={(option) => (
                  <div className="flex items-center justify-between">
                    <span>{option.label}</span>
                    {option.value !== '' && (
                      <Button
                        type="text"
                        danger
                        size="small"
                        icon={<DeleteOutlined />}
                        onMouseDown={(event) => {
                          event.preventDefault();
                          event.stopPropagation();
                        }}
                        onClick={(event) => {
                          event.stopPropagation();
                          deleteResolution(
                            resolutions.findIndex(
                              (resolution) => resolutionKey(resolution) === option.value
                            )
                          );
                        }}
                      />
                    )}
                  </div>
                )}
                onChange={applyOriginalResolution}
              />
              <Tooltip title={t('screen.controlRegion.addResolution')}>
                <Button icon={<PlusOutlined />} onClick={() => setIsAddOpen(true)} />
              </Tooltip>
            </Space.Compact>
          </>
        )}
      </Space>
    </div>
  );

  return (
    <>
      {contextHolder}
      <Popover
        content={content}
        placement="rightTop"
        arrow={false}
        align={{ offset: [14, 0] }}
        open={isPopoverOpen}
        onOpenChange={handleOpenChange}
      >
        <div className="flex h-[30px] cursor-pointer items-center space-x-2 rounded px-3 text-neutral-300 hover:bg-neutral-700/70">
          <ScanSearchIcon size={18} />
          <span className="select-none text-sm">{t('screen.controlRegion.title')}</span>
        </div>
      </Popover>
      <Modal
        title={t('screen.controlRegion.addResolution')}
        open={isAddOpen}
        okText={t('screen.controlRegion.add')}
        cancelText={t('screen.controlRegion.cancel')}
        onOk={addResolution}
        onCancel={() => setIsAddOpen(false)}
      >
        <Space.Compact block>
          <InputNumber<number>
            className="w-full"
            min={1}
            precision={0}
            value={newWidth}
            placeholder={t('screen.controlRegion.width')}
            onChange={setNewWidth}
          />
          <InputNumber<number>
            className="w-full"
            min={1}
            precision={0}
            value={newHeight}
            placeholder={t('screen.controlRegion.height')}
            onChange={setNewHeight}
          />
        </Space.Compact>
      </Modal>
    </>
  );
};
