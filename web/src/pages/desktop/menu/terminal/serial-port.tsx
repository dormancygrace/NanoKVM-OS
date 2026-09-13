import { ChangeEvent, useState } from 'react';
import { Button, Input, InputNumber, Modal, Radio, RadioChangeEvent, Select } from 'antd';
import { useSetAtom } from 'jotai';
import { SquareTerminalIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { openSerialTerminal } from '@/pages/terminal/launch.ts';

export const SerialPort = () => {
  const { t } = useTranslation();
  const setKeyboardLock = useSetAtom(keyboardLockAtom);

  const [isModalOpen, setIsModalOpen] = useState(false);
  const [port, setPort] = useState('');
  const [baudrate, setBaudrate] = useState(115200);
  const [parity, setParity] = useState<string>('none');
  const [flowControl, setFlowControl] = useState<string>('none');
  const [dataBits, setDataBits] = useState(8);
  const [stopBits, setStopBits] = useState(1);

  const isUSBSerial = /^\/dev\/ttyGS[0-9]+$/.test(port.trim());

  function openModal() {
    setKeyboardLock({ source: 'serial-port-modal', locked: true });
    setIsModalOpen(true);
  }

  function closeModal() {
    setKeyboardLock({ source: 'serial-port-modal', locked: false });
    setIsModalOpen(false);
  }

  function handleInputChange(e: ChangeEvent<HTMLInputElement>) {
    setPort(e.target.value);
  }

  function handleRadioChange(e: RadioChangeEvent) {
    setPort(e.target.value);
  }

  function onInputNumberChange(value: number | null) {
    if (value === null) return;
    setBaudrate(value);
  }

  function submit() {
    if (!port || !baudrate) {
      closeModal();
      return;
    }

    setKeyboardLock({ source: 'serial-port-modal', locked: false });
    setIsModalOpen(false);
    openSerialTerminal(
      new URLSearchParams(
        isUSBSerial
          ? { port: port.trim() }
          : {
              port: port.trim(),
              baud: String(baudrate),
              parity,
              flowControl,
              dataBits: String(dataBits),
              stopBits: String(stopBits)
            }
      ).toString()
    );
  }

  return (
    <>
      <div
        className="flex h-[28px] cursor-pointer items-center space-x-1 rounded px-2 py-1 select-none hover:bg-neutral-700/70"
        onClick={openModal}
      >
        <SquareTerminalIcon size={14} />
        <span>{t('terminal.serial')}</span>
      </div>

      <Modal open={isModalOpen} title={t('terminal.serial')} footer={null} onCancel={closeModal}>
        <div className="mt-10 flex items-center space-x-[20px]">
          <div className="flex w-[80px] justify-end text-neutral-400">
            {t('terminal.serialPort')}
          </div>
          <div className="w-1/2">
            <Input
              value={port}
              placeholder={t('terminal.serialPortPlaceholder')}
              onChange={handleInputChange}
            />
          </div>
        </div>
        <div className="mt-3 pl-[100px]">
          <Radio.Group size="large" onChange={handleRadioChange}>
            <Radio value="/dev/ttyS1">
              <code>/dev/ttyS1</code>
            </Radio>
            <Radio value="/dev/ttyS2">
              <code>/dev/ttyS2</code>
            </Radio>
          </Radio.Group>
        </div>

        {isUSBSerial ? (
          <p className="mt-7 text-neutral-400">
            {t('terminal.usbSpeed', {
              defaultValue: 'Virtual USB serial — baud rate does not limit transfer speed.'
            })}
          </p>
        ) : (
          <>
            <div className="mt-7 flex items-center space-x-[20px]">
              <div className="flex w-[80px] justify-end text-neutral-400">
                {t('terminal.baudrate')}
              </div>
              <InputNumber
                controls={false}
                min={1}
                defaultValue={115200}
                onChange={onInputNumberChange}
              />
            </div>

            <div className="mt-7 flex items-center space-x-[20px]">
              <div className="flex w-[80px] justify-end text-neutral-400">
                {t('terminal.parity')}
              </div>
              <div className="w-1/2">
                <Select
                  defaultValue="none"
                  value={parity}
                  onChange={setParity}
                  options={[
                    { label: t('terminal.parityNone'), value: 'none' },
                    { label: t('terminal.parityEven'), value: 'even' },
                    { label: t('terminal.parityOdd'), value: 'odd' }
                  ]}
                />
              </div>
            </div>

            <div className="mt-7 flex items-center space-x-[20px]">
              <div className="flex w-[80px] justify-end text-neutral-400">
                {t('terminal.flowControl')}
              </div>
              <div className="w-1/2">
                <Select
                  defaultValue="none"
                  value={flowControl}
                  onChange={setFlowControl}
                  options={[
                    { label: t('terminal.flowControlNone'), value: 'none' },
                    { label: t('terminal.flowControlSoft'), value: 'soft' },
                    { label: t('terminal.flowControlHard'), value: 'hard' }
                  ]}
                />
              </div>
            </div>

            <div className="mt-7 flex items-center space-x-[20px]">
              <div className="flex w-[80px] justify-end text-neutral-400">
                {t('terminal.dataBits')}
              </div>
              <div className="w-1/2">
                <Select
                  defaultValue={8}
                  value={dataBits}
                  onChange={setDataBits}
                  options={[
                    { label: '5', value: 5 },
                    { label: '6', value: 6 },
                    { label: '7', value: 7 },
                    { label: '8', value: 8 }
                  ]}
                />
              </div>
            </div>

            <div className="mt-7 flex items-center space-x-[20px]">
              <div className="flex w-[80px] justify-end text-neutral-400">
                {t('terminal.stopBits')}
              </div>
              <div className="w-1/2">
                <Select
                  defaultValue={1}
                  value={stopBits}
                  onChange={setStopBits}
                  options={[
                    { label: '1', value: 1 },
                    { label: '2', value: 2 }
                  ]}
                />
              </div>
            </div>
          </>
        )}

        <div className="mt-12 mb-3 flex justify-center">
          <Button type="primary" onClick={submit}>
            {t('terminal.confirm')}
          </Button>
        </div>
      </Modal>
    </>
  );
};
