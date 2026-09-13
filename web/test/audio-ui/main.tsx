import { useRef, useState } from 'react';
import { StyleProvider } from '@ant-design/cssinjs';
import { ConfigProvider, theme } from 'antd';
import { useSetAtom } from 'jotai';
import { createRoot } from 'react-dom/client';

import { MobileMenuItemProvider } from '../../src/components/menu-item';
import { menuCloseSignalAtom } from '../../src/jotai/settings';
import { AudioMenu, type useUsbAudio } from '../../src/pages/desktop/menu/audio';

import '../../src/i18n';
import '../../src/assets/styles/index.css';

function Fixture() {
  const [playing, setPlaying] = useState(false);
  const [available, setAvailable] = useState(true);
  const [volume, setVolume] = useState(80);
  const [visible, setVisible] = useState(true);
  const [mobile, setMobile] = useState(false);
  const close = useSetAtom(menuCloseSignalAtom);
  const playback = useRef<ReturnType<typeof useUsbAudio>['playback']['current']>(undefined);
  const audio = {
    available,
    playing,
    wanted: playing,
    blocked: false,
    busy: false,
    error: '',
    receiving: playing,
    volume,
    setVolume,
    playback,
    start: async () => setPlaying(true),
    stop: () => setPlaying(false)
  };
  return (
    <StyleProvider layer>
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <main style={{ padding: 24, color: 'white' }}>
          <h1>Audio menu fixture</h1>
          <p>Local controls only; no capture or connection to the device.</p>
          <button
            onClick={() => {
              close((v) => v + 1);
              setVisible((v) => !v);
            }}
          >
            Toggle toolbar
          </button>{' '}
          <button onClick={() => setMobile((v) => !v)}>Toggle mobile layout</button>{' '}
          <button onClick={() => setAvailable((v) => !v)}>Toggle USB audio</button>
          <output style={{ display: 'block', padding: 16 }}>
            Playback: {playing ? 'on' : 'off'}; volume: {volume}%
          </output>
          <div style={{ visibility: visible ? 'visible' : 'hidden', width: 40, margin: '32px 0' }}>
            <MobileMenuItemProvider
              mobilePlacement={mobile ? 'left' : undefined}
              mobilePopoverClassName={mobile ? 'nanokvm-mobile-menu-popover' : undefined}
            >
              <AudioMenu audio={audio} />
            </MobileMenuItemProvider>
          </div>
          <button style={{ marginTop: 240 }}>Outside popup</button>
        </main>
      </ConfigProvider>
    </StyleProvider>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
