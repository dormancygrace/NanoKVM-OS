import { getAccount } from '@/api/auth';
import { addShortcut } from '@/api/hid';
import { getEthernet, getSavedWifi, setEthernet } from '@/api/network';
import { http, type ApiResponse } from '@/lib/http';

// Compiled, never executed or included in the app bundle.
export async function responseContracts() {
  const opaque = await http.get('/api/unspecified');
  // @ts-expect-error Unspecified response data must be narrowed before use.
  void opaque.data.name;
  const account = await getAccount();
  // @ts-expect-error Account responses must not silently accept misspelled fields.
  void account.data.usernmae;
  const ethernet = await getEthernet();
  const trial = await setEthernet(ethernet.data);
  const token: string = trial.data.trial.token;
  // @ts-expect-error Network trials are nested, not top-level response data.
  void trial.data.token;
  const saved = await getSavedWifi();
  const ssid: string | undefined = saved.data[0]?.ssid;
  // @ts-expect-error Saved Wi-Fi is an array, not a wrapped object.
  void saved.data.networks;
  // @ts-expect-error Shortcut keys use a physical code, not a key field.
  addShortcut([{ label: 'A', key: 'KeyA' }]);
  const blob: Blob = await http.request({ url: '/api/archive', responseType: 'blob' });
  // @ts-expect-error Downloads do not return a JSON envelope.
  const json: ApiResponse = await http.request({ url: '/api/archive', responseType: 'blob' });
  return { token, ssid, blob, json };
}
