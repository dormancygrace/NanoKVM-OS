import { useState } from 'react';
import { Button, Modal } from 'antd';
import clsx from 'clsx';
import { ClapperboardIcon, PauseIcon, PlayIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export const Credits = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.about.credits.${key}`);
  const [open, setOpen] = useState(false);
  const [paused, setPaused] = useState(false);

  const entries = [
    { name: 'dormancygrace', contribution: tr('projectLead') },
    { name: 'wenjie', contribution: tr('wenjie') },
    { name: 'Z2Z-GuGu', contribution: tr('z2zGuGu') },
    { name: 'Alexander-Ger-Reich', contribution: tr('alexanderGerReich') },
    { name: 'AndrewMoryakov', contribution: tr('andrewMoryakov') },
    { name: 'watermeko', contribution: tr('watermeko') },
    { name: 'scpcom', contribution: tr('scpcom') },
    { name: 'LingkongSky', contribution: tr('lingkongSky') },
    { name: 'polyzium', contribution: tr('polyzium') },
    { name: 'Yury Pekishev', contribution: tr('yuryPekishev') },
    { name: 'Thomas Pressnell', contribution: tr('thomasPressnell') },
    { name: 'S33G', contribution: tr('s33g') },
    { name: 'gxcreator (Nikita S.)', contribution: tr('gxcreator') },
    { name: 'yuzi-co / IronKVM', contribution: tr('yuziCo') },
    { name: 'Sipeed NanoKVM', contribution: tr('sipeed') },
    { name: 'SOPHGO / CVITEK', contribution: tr('silicon') },
    { name: 'Linux · Buildroot · Alpine Linux · APK Tools', contribution: tr('systemStack') },
    { name: 'Go · Pion · React · Ant Design · OpenSSL · Opus', contribution: tr('appStack') },
    { name: 'Radxa · PiKVM · OneKVM', contribution: tr('projects') },
    { name: tr('contributorsTitle'), contribution: tr('contributors') }
  ];

  function show() {
    setPaused(false);
    setOpen(true);
  }

  return (
    <>
      <Button type="primary" icon={<ClapperboardIcon size={17} />} onClick={show}>
        {tr('button')}
      </Button>
      <Modal
        open={open}
        centered
        width={620}
        title={tr('title')}
        destroyOnHidden
        onCancel={() => setOpen(false)}
        footer={[
          <Button
            key="pause"
            icon={paused ? <PlayIcon size={16} /> : <PauseIcon size={16} />}
            onClick={() => setPaused((value) => !value)}
          >
            {paused ? tr('resume') : tr('pause')}
          </Button>,
          <Button key="close" type="primary" onClick={() => setOpen(false)}>
            {tr('close')}
          </Button>
        ]}
      >
        <div className="nanokvm-credits-viewport">
          <div
            className={clsx('nanokvm-credits-roll', paused && 'is-paused')}
            onAnimationEnd={(event) => {
              if (event.animationName === 'nanokvm-credits-roll') setOpen(false);
            }}
          >
            <img
              src="/nanokvm-os-connection.svg?v=3"
              alt="NanoKVM OS"
              className="mx-auto block size-20"
            />
            <h2 className="mt-5 text-center text-2xl font-semibold text-neutral-100">NanoKVM OS</h2>
            <p className="mt-2 text-center text-sm text-neutral-400">{tr('madePossibleBy')}</p>

            <div className="mt-14 space-y-12">
              {entries.map((entry) => (
                <section key={entry.name} className="space-y-2 text-center">
                  <h3 className="text-lg font-medium text-neutral-100">{entry.name}</h3>
                  <p className="mx-auto max-w-md text-sm leading-relaxed text-neutral-400">
                    {entry.contribution}
                  </p>
                </section>
              ))}
            </div>

            <p className="mt-16 text-center text-base font-medium text-neutral-200">
              {tr('closing')}
            </p>
          </div>
        </div>
      </Modal>
    </>
  );
};
