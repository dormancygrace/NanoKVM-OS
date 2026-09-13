import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Slider, Switch } from 'antd';
import { Volume2Icon, VolumeXIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { stereoOffer } from '@/lib/audio-sdp.ts';
import { http } from '@/lib/http.ts';
import { MenuItem } from '@/components/menu-item.tsx';

type Playback = {
  context: AudioContext;
  gain: GainNode;
  source?: MediaStreamAudioSourceNode;
  renderer?: HTMLAudioElement;
  statsTimer?: number;
  peer?: RTCPeerConnection;
  socket?: WebSocket;
  timer?: number;
};

const preferenceKey = 'nanokvm.usb-audio';
function readPreference(): { enabled: boolean; volume: number } {
  try {
    const value = JSON.parse(localStorage.getItem(preferenceKey) ?? '{}');
    return {
      enabled: value.enabled === true,
      volume:
        typeof value.volume === 'number' && Number.isFinite(value.volume)
          ? Math.max(0, Math.min(100, value.volume))
          : 80
    };
  } catch {
    return { enabled: false, volume: 80 };
  }
}

export const useUsbAudio = () => {
  const [available, setAvailable] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [receiving, setReceiving] = useState(false);
  const [wanted, setWanted] = useState(() => readPreference().enabled);
  const [volume, setVolume] = useState(() => readPreference().volume);
  const [blocked, setBlocked] = useState(false);
  const startRef = useRef<((automatic?: boolean) => Promise<void>) | undefined>(undefined);
  useEffect(() => {
    try {
      localStorage.setItem(preferenceKey, JSON.stringify({ enabled: wanted, volume }));
    } catch {
      /* Storage may be unavailable. */
    }
  }, [wanted, volume]);
  const playback = useRef<Playback | undefined>(undefined);
  const volumeRef = useRef(volume);
  volumeRef.current = volume;
  const disconnect = useCallback(() => {
    const old = playback.current;
    playback.current = undefined;
    window.clearTimeout(old?.timer);
    window.clearInterval(old?.statsTimer);
    if (old?.renderer) {
      old.renderer.pause();
      old.renderer.srcObject = null;
    }
    setReceiving(false);
    old?.socket?.close();
    old?.peer?.close();
    old?.source?.disconnect();
    old?.gain.disconnect();
    if (old) void old.context.close();
    setPlaying(false);
    setBusy(false);
  }, []);

  useEffect(() => {
    let alive = true;
    const refresh = async () => {
      try {
        const rsp = await http.get('/api/stream/audio/status');
        if (!alive || rsp.code !== 0) return;
        const enabled = Boolean(rsp.data.audio);
        setAvailable(enabled);
        if (!enabled) disconnect();
      } catch {
        /* Preserve the last known composition during a transient disconnect. */
      }
    };
    void refresh();
    const timer = window.setInterval(refresh, 5000);
    window.addEventListener(usbCompositionChangedEvent, refresh);
    window.addEventListener('nanokvm:usb-updated', refresh);
    return () => {
      alive = false;
      window.clearInterval(timer);
      window.removeEventListener(usbCompositionChangedEvent, refresh);
      window.removeEventListener('nanokvm:usb-updated', refresh);
      disconnect();
    };
  }, [disconnect]);

  const stop = useCallback(() => {
    setWanted(false);
    setBlocked(false);
    setError('');
    disconnect();
  }, [disconnect]);

  useEffect(() => {
    if (!wanted || !available || blocked || error === 'session') return;
    const attempt = () => {
      if (!playback.current) void startRef.current?.(true);
    };
    attempt();
    const retry = window.setInterval(attempt, 2000);
    return () => window.clearInterval(retry);
  }, [wanted, available, blocked, error]);

  async function start(automatic = false) {
    if (!automatic) {
      setWanted(true);
      setBlocked(false);
    }
    if (playback.current) {
      if (!automatic) void playback.current.context.resume();
      return;
    }
    setBusy(true);
    setError('');
    let current: Playback | undefined;
    try {
      // Restore listening when autoplay permits; otherwise require an explicit resume gesture.
      const context = new AudioContext({ latencyHint: 'interactive' });
      const gain = context.createGain();
      gain.gain.value = volumeRef.current / 100;
      gain.connect(context.destination);
      current = { context, gain };
      playback.current = current;
      const active = current;
      const fail = (reason = 'connection') => {
        if (playback.current === active) {
          setError(reason);
          disconnect();
        }
      };
      active.timer = window.setTimeout(() => fail('timeout'), 30000);
      const resumed = context.resume();
      if (automatic && context.state !== 'running') {
        setBlocked(true);
        setBusy(false);
      }
      await resumed;
      setBlocked(false);
      if (playback.current !== active) return;
      const url = new URL('/api/stream/audio', window.location.href);
      url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const socket = new WebSocket(url);
      active.socket = socket;
      const send = (event: string, data: unknown) => {
        if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ event, data }));
      };
      let pending: RTCIceCandidateInit[] = [];
      // Serialize answer and candidate application across asynchronous browser APIs.
      let messages = Promise.resolve();
      socket.onmessage = ({ data }) => {
        messages = messages
          .then(async () => {
            if (playback.current !== active) return;
            const message = JSON.parse(data);
            if (message.event === 'ice-servers') {
              if (active.peer) throw new Error('duplicate initialization');
              const peer = new RTCPeerConnection({ iceServers: message.data });
              active.peer = peer;
              let offerSent = false;
              const localCandidates: RTCIceCandidateInit[] = [];
              peer.onicecandidate = ({ candidate }) => {
                if (!candidate) return;
                if (offerSent) send('candidate', candidate.toJSON());
                else localCandidates.push(candidate.toJSON());
              };
              peer.onconnectionstatechange = () => {
                if (playback.current !== active) return;
                if (peer.connectionState === 'connected') {
                  window.clearTimeout(active.timer);
                  setPlaying(true);
                  setBusy(false);
                } else if (peer.connectionState === 'failed' || peer.connectionState === 'closed')
                  fail('connection');
              };
              let lastPackets = 0;
              active.statsTimer = window.setInterval(() => {
                void peer
                  .getStats()
                  .then((reports) => {
                    if (playback.current !== active) return;
                    let packets = 0;
                    reports.forEach((report) => {
                      if (
                        report.type === 'inbound-rtp' &&
                        report.kind === 'audio' &&
                        report.packetsReceived > 0
                      )
                        packets += report.packetsReceived;
                    });
                    setReceiving(packets > lastPackets);
                    lastPackets = packets;
                  })
                  .catch(() => {});
              }, 1000);
              peer.ontrack = ({ track, streams }) => {
                if (playback.current !== active || track.kind !== 'audio') return;
                active.source?.disconnect();
                const stream = streams[0] ?? new MediaStream([track]);
                // Keep Chromium's remote audio renderer active when Web Audio owns the output.
                const renderer = active.renderer ?? new Audio();
                renderer.autoplay = true;
                renderer.muted = true;
                renderer.srcObject = stream;
                active.renderer = renderer;
                void renderer.play().catch(() => fail('playback'));
                active.source = context.createMediaStreamSource(stream);
                active.source.connect(gain);
              };
              peer.addTransceiver('audio', { direction: 'recvonly' });
              const offer = await peer.createOffer();
              if (playback.current !== active) return;
              offer.sdp = stereoOffer(offer.sdp ?? '');
              await peer.setLocalDescription(offer);
              if (playback.current !== active) return;
              send('offer', peer.localDescription);
              offerSent = true;
              localCandidates.forEach((candidate) => send('candidate', candidate));
            } else if (message.event === 'answer') {
              if (!active.peer) throw new Error('missing peer');
              await active.peer.setRemoteDescription(message.data);
              for (const candidate of pending) await active.peer.addIceCandidate(candidate);
              pending = [];
            } else if (message.event === 'candidate') {
              if (active.peer?.remoteDescription) await active.peer.addIceCandidate(message.data);
              else pending.push(message.data);
            } else if (message.event === 'error') fail('capture');
          })
          .catch(() => fail('signaling'));
      };
      socket.onerror = () => fail('signaling');
      socket.onclose = ({ code }) => fail(code === 4401 ? 'session' : 'closed');
    } catch {
      if (!current || playback.current === current) {
        setError('playback');
        disconnect();
      }
    }
  }

  startRef.current = start;
  return {
    available,
    playing,
    wanted,
    blocked,
    busy,
    error,
    receiving,
    volume,
    setVolume,
    playback,
    start,
    stop
  };
};

export const AudioMenu = ({ audio }: { audio: ReturnType<typeof useUsbAudio> }) => {
  const { t } = useTranslation();
  const {
    available,
    playing,
    wanted,
    blocked,
    busy,
    error,
    receiving,
    volume,
    setVolume,
    playback,
    start,
    stop
  } = audio;
  if (!available) return null;
  return (
    <MenuItem
      title={t('audio.title')}
      icon={
        playing && volume > 0 ? (
          <Volume2Icon size={22} className="text-emerald-400" />
        ) : (
          <VolumeXIcon size={22} />
        )
      }
      content={
        <div className="w-56">
          <div className="flex items-center justify-between gap-4">
            <span>{t('audio.listen')}</span>
            <Switch
              checked={wanted}
              loading={busy}
              onChange={(on) => (on ? void start() : stop())}
              aria-label={t('audio.listen')}
            />
          </div>
          {blocked && (
            <Button className="mt-2" onClick={() => void start()}>
              {t('audio.resume', { defaultValue: 'Resume audio' })}
            </Button>
          )}
          <div className="mt-3">
            {t('audio.volume')}: {volume}%
          </div>
          <Slider
            min={0}
            max={100}
            value={volume}
            aria-label={t('audio.volume')}
            onChange={(value) => {
              setVolume(value);
              const active = playback.current;
              if (active)
                active.gain.gain.setTargetAtTime(value / 100, active.context.currentTime, 0.01);
            }}
          />
          {playing && (
            <div role="status" className="text-xs text-neutral-400">
              {t(receiving ? 'audio.receiving' : 'audio.waiting')}
            </div>
          )}
          {error && (
            <div role="alert" className="text-xs text-amber-400">
              {t(`audio.errors.${error}`, { defaultValue: t('audio.failed') })}
            </div>
          )}
        </div>
      }
    />
  );
};
