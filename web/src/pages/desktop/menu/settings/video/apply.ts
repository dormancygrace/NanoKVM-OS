import { selectEncoderCodec, updateFrameDetect } from '@/api/stream';
import { applyVideoSettings } from '@/api/vm';
import { setEncoderCodec } from '@/lib/encoder';
import * as storage from '@/lib/localstorage';
import {
  draftChanges,
  settingsRequest,
  type VideoCapabilities,
  type VideoDraft
} from '@/lib/video-model';

type ApplyDeps = {
  applyVideoSettings: typeof applyVideoSettings;
  selectEncoderCodec: typeof selectEncoderCodec;
  updateFrameDetect: typeof updateFrameDetect;
  setEncoderCodec: typeof setEncoderCodec;
  storage: Pick<typeof storage, 'setVideoMode' | 'setDirectPlayback'>;
  reload: () => void;
  clearPlaybackOverrides: () => void;
};

const defaultApplyDeps: ApplyDeps = {
  applyVideoSettings,
  selectEncoderCodec,
  updateFrameDetect,
  setEncoderCodec,
  storage,
  reload: () => window.location.reload(),
  // An explicit choice replaces any diagnostic render override in the URL.
  clearPlaybackOverrides: () => {
    const url = new URL(window.location.href);
    url.searchParams.delete('directRender');
    url.searchParams.delete('directBufferMs');
    window.history.replaceState(window.history.state, '', url);
  }
};

type ApplyResult = {
  // Shared settings were saved; null when only this browser changed.
  capabilities: VideoCapabilities | null;
  monitorRewritten: boolean;
  reloading: boolean;
  // The device restarts with this video memory mode.
  restart?: string;
};

// Make a transport the device transport without changing this browser.
export async function setDeviceTransport(
  transport: VideoDraft['transport'],
  apply: typeof applyVideoSettings = applyVideoSettings
) {
  const rsp = await apply({ transport });
  if (rsp.code !== 0) throw new Error(rsp.msg || 'video-settings-failed');
}

// Storage names the WebRTC transport after its original H.264 player.
const storedMode = (transport: VideoDraft['transport']) =>
  transport === 'webrtc' ? 'h264' : transport;

/*
 * Apply a draft: the shared settings (administrators only) as one validated
 * request, then this browser's player choices. The page reloads when the
 * player changes. Users change only the transport and the Direct playback.
 * With fitVideoMemory the device may switch the video memory mode and
 * restart: the other device settings go first then, as the restart follows
 * the response at once.
 */
export async function applyVideoDraft(
  saved: VideoDraft,
  draft: VideoDraft,
  caps: VideoCapabilities,
  options: { admin: boolean; confirmPowerCycle: boolean; fitVideoMemory?: boolean },
  deps: ApplyDeps = defaultApplyDeps
): Promise<ApplyResult> {
  const changes = draftChanges(saved, draft, caps);
  const result: ApplyResult = {
    capabilities: null,
    monitorRewritten: false,
    reloading: false
  };
  if (options.admin) {
    const fit = !!options.fitVideoMemory;
    const shared = async () => {
      const body = settingsRequest(saved, draft);
      if (Object.keys(body).length === 0) return;
      const rsp = await deps.applyVideoSettings(body, options.confirmPowerCycle, fit);
      if (rsp.code !== 0) throw new Error(rsp.msg || 'video-settings-failed');
      if (rsp.data?.restart) {
        result.restart = String(rsp.data.videoMemoryMode ?? '');
      } else {
        result.capabilities = rsp.data ?? null;
      }
      result.monitorRewritten = changes.monitorRewrite;
    };
    if (!fit) await shared();
    if (draft.frameDetect !== saved.frameDetect) {
      const rsp = await deps.updateFrameDetect(draft.frameDetect);
      if (rsp.code !== 0) throw new Error(rsp.msg || 'video-settings-failed');
    }
    if (
      draft.transport !== 'mjpeg' &&
      (draft.codec !== saved.codec || draft.transport !== saved.transport)
    ) {
      const rsp = await deps.selectEncoderCodec(draft.codec);
      if (rsp.code !== 0) throw new Error(rsp.msg || 'video-settings-failed');
    }
    if (fit) await shared();
    if (draft.codec !== saved.codec) deps.setEncoderCodec(draft.codec);
  }
  if (draft.directPlayback !== saved.directPlayback) {
    deps.storage.setDirectPlayback(draft.directPlayback);
    deps.clearPlaybackOverrides();
  }
  if (draft.transport !== saved.transport) deps.storage.setVideoMode(storedMode(draft.transport));
  const reconnect =
    draft.transport !== saved.transport ||
    (options.admin && draft.transport !== 'mjpeg' && draft.codec !== saved.codec) ||
    (draft.transport === 'direct' && draft.directPlayback !== saved.directPlayback);
  // No reload while the device restarts.
  if (reconnect && result.restart === undefined) {
    result.reloading = true;
    deps.reload();
  }
  return result;
}
