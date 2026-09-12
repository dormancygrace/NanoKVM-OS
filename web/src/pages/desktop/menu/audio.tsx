import { useCallback, useEffect, useRef, useState } from 'react';
import { Slider, Switch } from 'antd';
import { MenuItem } from '@/components/menu-item.tsx';
import { Volume2Icon, VolumeXIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { stereoOffer } from '@/lib/audio-sdp.ts';
import { http } from '@/lib/http.ts';

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

export const useUsbAudio = () => {
  const [available, setAvailable] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [receiving, setReceiving] = useState(false);
  const [volume, setVolume] = useState(80);
  const playback = useRef<Playback>();
  const volumeRef = useRef(volume);
  volumeRef.current = volume;
  const stop = useCallback(() => {
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
        if (!enabled) stop();
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
      stop();
    };
  }, [stop]);

  async function start() {
    if (playback.current) return;
    setBusy(true);
    setError('');
    let current: Playback | undefined;
    try {
      // Resume synchronously from the user gesture; no autoplay or microphone permission.
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
          stop();
        }
      };
      active.timer = window.setTimeout(() => fail('timeout'), 30000);
      await context.resume();
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
              active.statsTimer = window.setInterval(() => {
                void peer.getStats().then((reports) => {
                  if (playback.current !== active) return;
                  let received = false;
                  reports.forEach((report) => {
                    if (report.type === 'inbound-rtp' && report.kind === 'audio' && report.packetsReceived > 0)
                      received = true;
                  });
                  setReceiving(received);
                }).catch(() => {});
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
        stop();
      }
    }
  }

  return { available, playing, busy, error, receiving, volume, setVolume, playback, start, stop };
};

export const AudioMenu = ({ audio }: { audio: ReturnType<typeof useUsbAudio> }) => {
  const { t } = useTranslation();
  const { available, playing, busy, error, receiving, volume, setVolume, playback, start, stop } = audio;
  if (!available) return null;
  return (
    <MenuItem
      title={t('audio.title')}
      icon={playing && volume > 0 ? <Volume2Icon size={22} className="text-emerald-400" /> : <VolumeXIcon size={22} />}
      content={
        <div className="w-56">
          <div className="flex items-center justify-between gap-4">
            <span>{t('audio.listen')}</span>
            <Switch
              checked={playing}
              loading={busy}
              onChange={(on) => (on ? void start() : stop())}
              aria-label={t('audio.listen')}
            />
          </div>
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
