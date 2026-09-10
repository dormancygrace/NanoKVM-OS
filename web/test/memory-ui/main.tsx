import React, { useState } from 'react';
import { ConfigProvider, theme } from 'antd';
import ReactDOM from 'react-dom/client';

import i18n from '../../src/i18n';
import { Memory } from '../../src/pages/desktop/menu/settings/memory';

import '../../src/assets/styles/index.css';

function Fixture() {
  const [key, setKey] = useState(0);
  const [log, setLog] = useState('');
  const configure = async (mode: string, remount = false) => {
    await fetch('/__fixture/config', { method: 'POST', body: JSON.stringify({ mode }) });
    if (remount) setKey(key + 1);
  };
  return (
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
      <header style={{ background: '#2b3240', padding: 12, color: 'white' }}>
        <strong>Memory UI fixture — local API only, no device connection</strong>
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', paddingTop: 8 }}>
          <button onClick={() => configure('healthy', true)}>Reset fixture</button>
          <button onClick={() => configure('read-failure')}>Fail reads</button>
          <button onClick={() => configure('healthy')}>Recover reads</button>
          <button onClick={() => configure('slow', true)}>Slow reads 4.5s</button>
          <button onClick={() => configure('write-failure')}>Fail changes</button>
          <button onClick={() => configure('unavailable', true)}>Unavailable ZRAM</button>
          <button onClick={() => i18n.changeLanguage('en')}>English</button>
          <button onClick={() => i18n.changeLanguage('ru')}>Русский</button>
          <button
            onClick={async () =>
              setLog(JSON.stringify(await (await fetch('/__fixture/log')).json()))
            }
          >
            Show request log
          </button>
        </div>
        <output style={{ display: 'block', overflowWrap: 'anywhere' }}>{log}</output>
      </header>
      <main style={{ maxWidth: 580, margin: '24px auto', padding: 12 }}>
        <Memory key={key} />
      </main>
    </ConfigProvider>
  );
}
ReactDOM.createRoot(document.getElementById('root')!).render(<Fixture />);
