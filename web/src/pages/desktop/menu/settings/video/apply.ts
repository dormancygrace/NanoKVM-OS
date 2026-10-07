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

export type ApplyDeps = {
  applyVideoSettings: typeof applyVideoSettings;
  selectEncoderCodec: typeof selectEncoderCodec;
  updateFrameDetect: typeof updateFrameDetect;
  setEncoderCodec: typeof setEncoderCodec;
  storage: Pick<typeof storage, 'setVideoMode' | 'setDirectPlayback'>;
  reload: () => void;
  clearPlaybackOverrides: () => void;
};

export const defaultApplyDeps: ApplyDeps = {
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

export type ApplyResult = {
  // Shared settings were saved; null when only this browser changed.
  capabilities: VideoCapabilities | null;
  gopModeRestartRequired: boolean;
  monitorRewritten: boolean;
  reloading: boolean;
};

// Storage names the WebRTC transport after its original H.264 player.
export const storedMode = (transport: VideoDraft['transport']) =>
  transport === 'webrtc' ? 'h264' : transport;

/*
 * Apply a draft: the shared settings (administrators only) as one validated
 * request, then this browser's player choices. The page reloads when the
 * player changes. Users change only the transport and the Direct playback.
 */
export async function applyVideoDraft(
  saved: VideoDraft,
  draft: VideoDraft,
  caps: VideoCapabilities,
  options: { admin: boolean; confirmPowerCycle: boolean },
  deps: ApplyDeps = defaultApplyDeps
): Promise<ApplyResult> {
  const changes = draftChanges(saved, draft, caps);
  const result: ApplyResult = {
    capabilities: null,
    gopModeRestartRequired: false,
    monitorRewritten: false,
    reloading: false
  };
  if (options.admin) {
    const body = settingsRequest(saved, draft);
    if (Object.keys(body).length > 0) {
      const rsp = await deps.applyVideoSettings(body, options.confirmPowerCycle);
      if (rsp.code !== 0) throw new Error(rsp.msg || 'video-settings-failed');
      result.capabilities = rsp.data ?? null;
      result.monitorRewritten = changes.monitorRewrite;
      result.gopModeRestartRequired = 'gopMode' in body;
    }
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
  if (reconnect) {
    result.reloading = true;
    deps.reload();
  }
  return result;
}
