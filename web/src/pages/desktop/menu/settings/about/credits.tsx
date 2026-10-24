import { useState } from 'react';
import { Button, Modal } from 'antd';
import clsx from 'clsx';
import { ClapperboardIcon, HeartIcon, LanguagesIcon, PauseIcon, PlayIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export const Credits = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.about.credits.${key}`);
  const [open, setOpen] = useState(false);
  const [paused, setPaused] = useState(false);

  const entries = [
    { name: 'dormancygrace', contribution: tr('projectLead') },
    {
      name: 'neVERberleRfellerER',
      contribution: tr('neverberlerfellerer'),
      url: 'https://github.com/neVERberleRfellerER'
    },
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
    { name: 'rockymtngeek', contribution: tr('rockymtngeek') },
    { name: 'yuzi-co / IronKVM', contribution: tr('yuziCo') },
    { name: 'Sipeed NanoKVM', contribution: tr('sipeed') },
    { name: 'SOPHGO / CVITEK', contribution: tr('silicon') },
    { name: 'Linux · Buildroot · Alpine Linux · APK Tools', contribution: tr('systemStack') },
    { name: 'Go · Pion · React · Ant Design · OpenSSL · Opus', contribution: tr('appStack') },
    { name: 'Radxa · PiKVM', contribution: tr('projects') },
    { name: tr('contributorsTitle'), contribution: tr('contributors') }
  ];

  const translators = [
    { name: 'Pavel Borecki', language: 'Česky', url: 'https://github.com/p-bo' },
    {
      name: 'Alexander-Ger-Reich',
      language: 'Deutsch · English',
      url: 'https://github.com/Alexander-Ger-Reich'
    },
    { name: 'polyzium', language: 'Русский', url: 'https://github.com/polyzium' }
  ];

  function show() {
    setPaused(false);
    setOpen(true);
  }

  return (
    <>
      <Button type="primary" icon={<ClapperboardIcon size={16} />} onClick={show}>
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
            <h2 className="text-fg mt-5 text-center text-2xl font-semibold">NanoKVM OS</h2>
            <p className="text-fg-muted mt-2 text-center text-sm">{tr('madePossibleBy')}</p>

            <section className="border-line mt-12 rounded-xl border p-5 text-center">
              <HeartIcon aria-hidden="true" size={28} className="mx-auto text-[#f43f5e]" />
              <h3 className="text-fg mt-3 text-lg font-semibold">{tr('hardwareSupportTitle')}</h3>
              <a
                href="https://github.com/p-bo"
                target="_blank"
                rel="noopener noreferrer"
                className="text-info mt-4 inline-block rounded font-medium underline-offset-4 hover:underline focus-visible:outline focus-visible:outline-2"
              >
                Pavel Borecki
              </a>
              <p className="text-fg-muted mt-2 text-sm leading-relaxed">
                {tr('pavelHardwareThanks')}
              </p>
            </section>

            <section className="border-line mt-12 rounded-xl border p-5 text-center">
              <LanguagesIcon aria-hidden="true" size={28} className="text-info mx-auto" />
              <h3 className="text-fg mt-3 text-lg font-semibold">{tr('translationTitle')}</h3>
              <p className="text-fg-muted mt-2 text-sm leading-relaxed">
                {tr('translationThanks')}
              </p>
              <ul className="mt-6 list-none space-y-4 p-0">
                {translators.map((person) => (
                  <li key={person.url}>
                    <a
                      href={person.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="text-info rounded underline-offset-4 hover:underline focus-visible:outline focus-visible:outline-2"
                    >
                      {person.name}
                    </a>
                    <p className="text-fg-muted mt-1 text-sm">{person.language}</p>
                  </li>
                ))}
              </ul>
              <div className="border-line mt-6 border-t pt-5">
                <a
                  href="https://hosted.weblate.org/engage/nanokvm-os/"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-info rounded font-medium underline-offset-4 hover:underline focus-visible:outline focus-visible:outline-2"
                >
                  Hosted Weblate
                </a>
                <p className="text-fg-muted mt-2 text-sm leading-relaxed">
                  {tr('translationPlatform')}
                </p>
              </div>
            </section>

            <div className="mt-14 space-y-12">
              {entries.map((entry) => (
                <section key={entry.name} className="space-y-2 text-center">
                  <h3 className="text-fg text-lg font-medium">
                    {entry.url ? (
                      <a
                        href={entry.url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-info rounded underline-offset-4 hover:underline focus-visible:outline focus-visible:outline-2"
                      >
                        {entry.name}
                      </a>
                    ) : (
                      entry.name
                    )}
                  </h3>
                  <p className="text-fg-muted mx-auto max-w-md text-sm leading-relaxed">
                    {entry.contribution}
                  </p>
                </section>
              ))}
            </div>

            <p className="text-fg mt-16 text-center text-base font-medium">{tr('closing')}</p>
          </div>
        </div>
      </Modal>
    </>
  );
};
