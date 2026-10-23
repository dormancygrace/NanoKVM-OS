import { useCallback, useEffect, useRef, useState } from 'react';

import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { createAudioOutput } from '@/lib/audio-output.ts';
import { stereoOffer } from '@/lib/audio-sdp.ts';
import { refreshLiveStatus, subscribeLiveStatus } from '@/lib/live-status.ts';
import { ReconnectGate } from '@/lib/reconnect-delay.ts';
import { publishUsbAudioStream } from '@/lib/usb-audio-stream.ts';
import { getVideoAudio, watchVideoAudio, type VideoAudioChannel } from '@/lib/video-audio.ts';
import { recordAudioReceiver } from '@/lib/webrtc-audio-recording.ts';

type Playback = {
  context: AudioContext;
  gain: GainNode;
  source?: MediaStreamAudioSourceNode;
  renderer?: HTMLAudioElement;
  statsTimer?: number;
  peer?: RTCPeerConnection;
  recordingAudio?: ReturnType<typeof recordAudioReceiver>;
  socket?: WebSocket;
  // Set when listening through the WebRTC video connection.
  channel?: VideoAudioChannel;
  unsubscribe?: () => void;
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
  const output = useRef<ReturnType<typeof createAudioOutput> | undefined>(undefined);
  const closeOutput = useCallback(() => {
    output.current?.close();
    output.current = undefined;
  }, []);
  const volumeRef = useRef(volume);
  volumeRef.current = volume;
  const wantedRef = useRef(wanted);
  wantedRef.current = wanted;
  // Each failure sets the error, which re-runs the retry effect: without the
  // gate it reconnected at once, a new WebSocket and WebRTC setup each time.
  const gate = useRef(new ReconnectGate());
  const disconnect = useCallback(() => {
    const old = playback.current;
    playback.current = undefined;
    window.clearTimeout(old?.timer);
    window.clearInterval(old?.statsTimer);
    if (old?.renderer) {
      old.renderer.pause();
      old.renderer.srcObject = null;
    }
    if (old) publishUsbAudioStream(null);
    setReceiving(false);
    old?.unsubscribe?.();
    old?.channel?.stop();
    old?.socket?.close();
    old?.peer?.close();
    old?.recordingAudio?.close();
    old?.source?.disconnect();
    // Keep the authorized output alive while the transport reconnects.
    setPlaying(false);
    setBusy(false);
  }, []);

  useEffect(() => {
    const refresh = () => void refreshLiveStatus({ force: true });
    const unsubscribe = subscribeLiveStatus((live) => {
      // A failed read preserves the last known composition during a transient
      // disconnect.
      if (!live) return;
      const enabled = Boolean(live.audio.audio);
      setAvailable(enabled);
      if (!enabled) {
        // Without USB audio (RustDesk switches the composition, for one)
        // nothing feeds the output: release the audio device. The next start
        // makes a new output; after any interaction with the page it needs no
        // new gesture. Its blocked state goes with it, or the retry would wait
        // for a resume that no listener performs any more.
        disconnect();
        closeOutput();
        setBlocked(false);
        // Audio coming back with the composition is a fresh start.
        gate.current.connected();
      }
    });
    window.addEventListener(usbCompositionChangedEvent, refresh);
    window.addEventListener('nanokvm:usb-updated', refresh);
    return () => {
      unsubscribe();
      window.removeEventListener(usbCompositionChangedEvent, refresh);
      window.removeEventListener('nanokvm:usb-updated', refresh);
      disconnect();
      closeOutput();
    };
  }, [disconnect, closeOutput]);

  // Prefer the video connection: when it publishes an audio channel while
  // audio plays over the standalone connection, reconnect through it (the
  // retry below restarts playback). Same MediaStream as the video, so the
  // browser keeps lip sync.
  useEffect(
    () =>
      watchVideoAudio((channel) => {
        const active = playback.current;
        if (channel && active && !active.channel && wantedRef.current) disconnect();
      }),
    [disconnect]
  );

  const stop = useCallback(() => {
    wantedRef.current = false;
    setWanted(false);
    setBlocked(false);
    setError('');
    disconnect();
    closeOutput();
  }, [disconnect, closeOutput]);

  useEffect(() => {
    if (!wanted || !available || blocked || error === 'session') return;
    const attempt = () => {
      if (!playback.current && gate.current.allows(performance.now()))
        void startRef.current?.(true);
    };
    attempt();
    const retry = window.setInterval(attempt, 1000);
    return () => window.clearInterval(retry);
  }, [wanted, available, blocked, error]);

  async function start(automatic = false) {
    if (!automatic) {
      wantedRef.current = true;
      setWanted(true);
      setBlocked(false);
      gate.current.connected();
    }
    if (playback.current) {
      if (!automatic) void output.current?.resume().catch(() => {});
      return;
    }
    setBusy(true);
    setError('');
    let current: Playback | undefined;
    try {
      if (!output.current) output.current = createAudioOutput(setBlocked);
      const sink = output.current;
      const { context, gain } = sink;
      gain.gain.value = volumeRef.current / 100;
      current = { context, gain };
      playback.current = current;
      const active = current;
      const fail = (reason = 'connection') => {
        if (playback.current === active) {
          gate.current.failed(performance.now());
          setError(reason);
          disconnect();
        }
      };
      const resumed = sink.resume();
      if (context.state !== 'running') setBusy(false);
      await resumed;
      if (playback.current !== active) return;
      setBusy(true);
      // Autoplay may wait indefinitely for a gesture. Only time the connection
      // once audio is authorized, so a delayed first click still restores it.
      active.timer = window.setTimeout(() => fail('timeout'), 30000);
      const channel = getVideoAudio();
      if (channel) {
        listenThroughVideo(active, channel, fail);
        return;
      }
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
                  gate.current.connected();
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
                publishUsbAudioStream(stream);
                void renderer.play().catch(() => fail('playback'));
                active.source = context.createMediaStreamSource(stream);
                active.source.connect(gain);
              };
              const audioReceiver = peer.addTransceiver('audio', {
                direction: 'recvonly'
              }).receiver;
              active.recordingAudio = recordAudioReceiver(audioReceiver, () => fail('connection'));
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
        gate.current.failed(performance.now());
        setError('playback');
        disconnect();
      }
    }
  }

  function listenThroughVideo(
    active: Playback,
    channel: VideoAudioChannel,
    fail: (reason?: string) => void
  ) {
    active.channel = channel;
    active.unsubscribe = channel.subscribe((event) => {
      if (playback.current !== active) return;
      if (event === 'started') {
        window.clearTimeout(active.timer);
        gate.current.connected();
        setPlaying(true);
        setBusy(false);
      } else fail(event === 'closed' ? 'closed' : 'capture');
    });
    let lastPackets = 0;
    active.statsTimer = window.setInterval(() => {
      void channel.peer
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
    void channel.stream().then((stream) => {
      if (playback.current !== active) return;
      // Keep Chromium's remote audio renderer active when Web Audio owns the output.
      const renderer = active.renderer ?? new Audio();
      renderer.autoplay = true;
      renderer.muted = true;
      renderer.srcObject = stream;
      active.renderer = renderer;
      publishUsbAudioStream(stream);
      void renderer.play().catch(() => fail('playback'));
      active.source?.disconnect();
      active.source = active.context.createMediaStreamSource(stream);
      active.source.connect(active.gain);
    });
    channel.start();
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
