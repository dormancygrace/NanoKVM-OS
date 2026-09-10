import { useState } from 'react';
import ReactDOM from 'react-dom/client';
import { HelmetProvider } from 'react-helmet-async';
import '../../src/i18n';
import { Terminal } from '../../src/pages/terminal';
import '../../src/assets/styles/index.css';

function Fixture() {
  const [mounted, setMounted] = useState(true);
  const [log, setLog] = useState('');
  const [height, setHeight] = useState(360);
  const pending = async () => {
    setMounted(false);
    await fetch('/__fixture/delay', {method:'POST'});
    setMounted(true);
    setTimeout(()=>setMounted(false),50);
  };
  return <HelmetProvider>
    <header style={{padding:12,color:'white',background:'#263040'}}>
      <strong>Terminal fixture — local WebSocket only</strong>
      <div style={{display:'flex',gap:20}}>
        <button onClick={()=>setMounted(!mounted)}>{mounted?'Unmount':'Mount'}</button>
        <button onClick={()=>{setHeight(height===360?240:360); setTimeout(()=>window.dispatchEvent(new Event('resize')),0)}}>Resize terminal</button>
        <button onClick={pending}>Close while connecting</button>
        <button onClick={async()=>setLog(JSON.stringify(await (await fetch('/__fixture/log')).json()))}>Show log</button>
      </div>
      <output style={{display:'block',overflowWrap:'anywhere'}}>{log}</output>
    </header>
    <main style={{height}}>{mounted&&<Terminal/>}</main>
  </HelmetProvider>;
}
ReactDOM.createRoot(document.getElementById('root')!).render(<Fixture/>);
