import { addonCatalogAction, checkAddonCatalog, type AddonCatalog } from '@/api/addons';
import { getAccount } from '@/api/auth';
import { configureBmc, type BmcSettings } from '@/api/bmc';
import { addShortcut } from '@/api/hid';
import { getEthernet, getSavedWifi, setEthernet } from '@/api/network';
import { configureRustDesk, rustDeskPackageAction, type RustDeskConfig } from '@/api/rustdesk';
import { setUpdateNoticeEnabled } from '@/api/updates';
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

// Mutators that return OkRsp/updateReply(nil) must not promise status data.
export async function mutationContracts(bmc: BmcSettings, rustdesk: RustDeskConfig) {
  const checked: null = (await checkAddonCatalog()).data;
  const notice: null = (await setUpdateNoticeEnabled(true)).data;
  const configured: null = (await configureBmc(bmc)).data;
  const desk: null = (await configureRustDesk(rustdesk)).data;
  const action: null = (await rustDeskPackageAction('check')).data;
  const catalog: AddonCatalog = (await addonCatalogAction('install', 'nanokvm-tpu')).data;
  // @ts-expect-error A check acknowledges the request; it does not return a catalog.
  void (await checkAddonCatalog()).data.addons;
  return { checked, notice, configured, desk, action, catalog };
}
