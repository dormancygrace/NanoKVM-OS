const keyInput = document.querySelector('#key');
let key = '';
try { key = sessionStorage.getItem('nanokvm-standby-key') || ''; } catch {}
keyInput.value = key;
let restarting = false;
document.querySelector('#use').onclick = () => {
  key = keyInput.value.trim();
  try { sessionStorage.setItem('nanokvm-standby-key', key); } catch {}
};
document.querySelector('#copy').onclick = async () => {
  try {
    await navigator.clipboard.writeText(key);
    detail.textContent = 'Restart key copied. Keep it private.';
  } catch {
    keyInput.type = 'text';
    keyInput.select();
    detail.textContent = 'Copy the selected restart key and keep it private.';
  }
};
const status = document.querySelector('#state');
const detail = document.querySelector('#detail');
const start = document.querySelector('#start');
start.onclick = async () => {
  if (!confirm('Start NanoKVM OS? Keep power connected while it starts.')) return;
  start.disabled = true;
  restarting = true;
  status.textContent = "NanoKVM OS is starting. Keep power connected.";
  try {
    const rsp = await fetch('/standby/start', {method:'POST', headers:{Authorization:'Bearer '+key}});
    if (!rsp.ok) throw new Error(await rsp.text());
    restarting = true;
    status.textContent = 'NanoKVM OS is starting. Keep power connected.';
    setTimeout(() => location.assign('/'), 30000);
  } catch (error) { restarting = false; detail.textContent = String(error); start.disabled = false; setTimeout(poll, 1500); }
};
async function poll() {
  if (restarting) return;
  try {
    const rsp = await fetch('/standby/status', {headers:{Authorization:'Bearer '+key}, cache:'no-store', signal:AbortSignal.timeout(4000)});
    if (!rsp.ok) throw new Error(rsp.status === 401 ? 'Enter the restart key from the browser that requested shutdown.' : 'Shutdown checks are not available.');
    const s = await rsp.json();
    if (restarting) return;
    const safe = s.state === 'ready' && s.safe === true;
    status.textContent = safe ? 'Shutdown complete. You can disconnect NanoKVM power.' :
      s.state === 'failed' ? 'Shutdown could not be verified. Keep power connected.' :
      'Shutdown is being checked. Keep power connected.';
    detail.textContent = s.error || (safe ? 'NanoKVM keeps only its network restart service running. Ordinary functions are stopped.' : 'Do not disconnect power until the checks finish.');
    document.querySelector('#wake').textContent = s.magicWake
      ? 'Magic-packet restart is enabled for '+s.mac+'. Anyone on a reachable network can send it; no password is required.'
      : 'Use this page to start NanoKVM OS.';
    start.disabled = !(safe || s.state === 'failed');
  } catch (error) {
    if (restarting) return;
    status.textContent = 'Shutdown status is unavailable. Keep power connected until it is verified.';
    detail.textContent = String(error);
    start.disabled = true;
  } finally { if (!restarting) setTimeout(poll, 1500); }
}
poll();
