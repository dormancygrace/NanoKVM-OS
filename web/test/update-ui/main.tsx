import React, { useState } from 'react';
import { ConfigProvider, theme } from 'antd';
import ReactDOM from 'react-dom/client';

import i18n from '../../src/i18n';
import { Updates } from '../../src/pages/desktop/menu/settings/updates';

import '../../src/assets/styles/index.css';

void i18n.changeLanguage('en');
function Fixture() {
  const [key, setKey] = useState(0);
  const configure = async (mode: string) => {
    await fetch('/__fixture/' + mode, { method: 'POST' });
    setKey((k) => k + 1);
  };
  return (
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
      <header>
        Local update UI fixture — no device operations
        <button onClick={() => configure('prepare')}>Prepare test update</button>
        <button onClick={() => configure('failed')}>Simulate failure</button>
        <button onClick={() => setKey((k) => k + 1)}>Reopen settings</button>
      </header>
      <main style={{ maxWidth: 750, margin: '24px auto' }}>
        <Updates key={key} />
      </main>
    </ConfigProvider>
  );
}
ReactDOM.createRoot(document.getElementById('root')!).render(<Fixture />);
