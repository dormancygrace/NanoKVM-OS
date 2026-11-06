import { useEffect, useRef, useState } from 'react';
import { notification, Spin } from 'antd';
import clsx from 'clsx';
import { useAtomValue, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { stereoOffer } from '@/lib/audio-sdp.ts';
import { ConnectRecovery } from '@/lib/connect-recovery.ts';
import { encoderCodecQuery, getEncoderCodec, supportsWebRTCH265 } from '@/lib/encoder.ts';
import { publishRecordingSource } from '@/lib/recording-source.ts';
import { captureMediaElement } from '@/lib/screenshot.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { createVideoAudioChannel, getVideoAudio, publishVideoAudio } from '@/lib/video-audio.ts';
import { recordAudioReceiver } from '@/lib/webrtc-audio-recording.ts';
import { mouseStyleAtom } from '@/jotai/mouse.ts';
import { screenshotSourceAtom } from '@/jotai/screen.ts';

import { ScreenViewport } from './viewport.tsx';
import { startWebrtcDiagnostics } from './webrtc-diagnostics.ts';
import { canRecordReceiver, receiverRecording } from './webrtc-recording.ts';

type SignalingMessage = {
  event?: string;
  data?: string;
};

const WEBRTC_CONNECTION_FAILED_NOTIFICATION_KEY = 'webrtc_connection_failed';
const WEBRTC_CONNECTION_TIMEOUT = 10 * 1000;
const WEBRTC_RECONNECT_DELAY = 3 * 1000;
// A failed peer, and a signaling socket soon after a lost connection (see
// ConnectRecovery), retry sooner: after a device reboot, a new peer connects as
// soon as the server is back.
const WEBRTC_FAST_RECONNECT_DELAY = 500;
// In video recovery mode the server answers each signaling attempt with 503, so for
// the recovery window a visible page costs it about two TLS handshakes a second.
const signalingRecovery = new ConnectRecovery();

const parseSignalingData = <T,>(data?: string): T | null => {
  if (!data) {
    return null;
  }

  return JSON.parse(data) as T;
};

export const H264Webrtc = ({
  onEncoderConflict
}: {
  onEncoderConflict: (reconfigured?: boolean) => boolean;
}) => {
  const { t } = useTranslation();
  const mouseStyle = useAtomValue(mouseStyleAtom);
  const setScreenshotSource = useSetAtom(screenshotSourceAtom);
  const [isLoading, setIsLoading] = useState(true);
  const [connectionAttempt, setConnectionAttempt] = useState(0);
  const [notificationApi, contextHolder] = notification.useNotification();

  const videoRef = useRef<HTMLVideoElement | null>(null);
  const diagnosticsRef = useRef<HTMLOutputElement | null>(null);
  const diagnostics = new URLSearchParams(window.location.search).get('webrtcStats') === '1';
  const videoOfferSent = useRef(false);
  const videoIceCandidates = useRef<RTCIceCandidate[]>([]);
  const translationRef = useRef(t);

  const updateScreenshotSource = () => {
    const element = videoRef.current;
    if (!element || element.readyState < HTMLMediaElement.HAVE_CURRENT_DATA) return;
    const { videoWidth: width, videoHeight: height } = element;
    if (!width || !height) return;
    setScreenshotSource({ width, height, capture: () => captureMediaElement(element) });
  };

  useEffect(() => {
    translationRef.current = t;
  }, [t]);

  useEffect(() => {
    setScreenshotSource(null);
    const requestedCodec = getEncoderCodec();
    const codec = requestedCodec === 'h265' && !supportsWebRTCH265() ? 'h264' : requestedCodec;
    const query = encoderCodecQuery(codec);
    const url = `${getBaseUrl('ws')}/api/stream/video?${query}`;
    const signalingCreatedAt = Date.now();
    const ws = new WebSocket(url);
    let signalingOpened = false;
    const connectTimeout = signalingRecovery.connectTimeout(signalingCreatedAt);
    const signalingConnectTimer =
      connectTimeout === null
        ? undefined
        : setTimeout(() => {
            if (ws.readyState === WebSocket.CONNECTING) ws.close();
          }, connectTimeout);
    const videoElement = videoRef.current;

    let video: RTCPeerConnection | null = null;
    // USB audio rides on this connection; see lib/video-audio.ts.
    let audioChannel: ReturnType<typeof createVideoAudioChannel> | null = null;
    let audioTransceiver: RTCRtpTransceiver | null = null;
    let recordingAudio: ReturnType<typeof recordAudioReceiver> = null;
    const closeAudioChannel = () => {
      if (!audioChannel) return;
      if (getVideoAudio() === audioChannel) publishVideoAudio(null);
      audioChannel.emit('closed');
      audioChannel = null;
      audioTransceiver = null;
    };
    // Recordings take the received frames as they are; see webrtc-recording.ts.
    // Closed after the connection.
    let recording: ReturnType<typeof receiverRecording> | null = null;
    const closeRecording = () => {
      recordingAudio?.close();
      recordingAudio = null;
      if (!recording) return;
      publishRecordingSource(null);
      recording.close();
      recording = null;
    };
    let heartbeatTimer: ReturnType<typeof setInterval> | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let stopDiagnostics: (() => void) | null = null;
    let disposed = false;
    let terminalFailure = false;

    const showConnectionFailureNotification = () => {
      notificationApi.error({
        key: WEBRTC_CONNECTION_FAILED_NOTIFICATION_KEY,
        message: translationRef.current('screen.webrtcConnectionFailed.title'),
        description: translationRef.current('screen.webrtcConnectionFailed.description'),
        placement: 'topRight',
        closable: false,
        duration: 0
      });
    };

    const cancelReconnect = () => {
      if (!reconnectTimer) {
        return;
      }

      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    };

    // A signaling socket that had opened ended, or the peer on it failed; a socket this
    // page closed itself (unmount, codec change) was not lost.
    const signalingLost = () => {
      if (signalingOpened && !disposed) signalingRecovery.lost(Date.now());
    };

    const scheduleReconnect = (reason: string, error?: unknown, fast = false) => {
      if (disposed || terminalFailure) {
        return;
      }

      console.error(`WebRTC connection failed: ${reason}`, error);
      showConnectionFailureNotification();
      setIsLoading(false);

      if (reconnectTimer) {
        return;
      }

      reconnectTimer = setTimeout(
        () => {
          reconnectTimer = null;
          setConnectionAttempt((attempt) => attempt + 1);
        },
        // Like WsClient, a hidden page keeps the backoff while recovering.
        fast || (document.visibilityState === 'visible' && signalingRecovery.recovering(Date.now()))
          ? WEBRTC_FAST_RECONNECT_DELAY
          : WEBRTC_RECONNECT_DELAY
      );
    };

    const sendMsg = (event: string, data: string) => {
      if (ws.readyState !== WebSocket.OPEN) {
        return;
      }

      try {
        ws.send(JSON.stringify({ event, data }));
      } catch (err) {
        console.error('Error sending event: ', err);
      }
    };

    const startVideo = (iceServers: RTCIceServer[]) => {
      if (video || disposed) {
        return;
      }

      const peer = new RTCPeerConnection({ iceServers });
      video = peer;
      if (diagnostics && diagnosticsRef.current)
        stopDiagnostics = startWebrtcDiagnostics(peer, diagnosticsRef.current, videoElement);
      videoOfferSent.current = false;
      videoIceCandidates.current = [];

      // --- Init Video ---
      peer.onnegotiationneeded = async () => {
        if (videoOfferSent.current || peer.signalingState !== 'stable') {
          console.log('Skipping video negotiation - Waiting for answer or state unstable');
          return;
        }

        try {
          videoOfferSent.current = true;
          // Explicit transceivers: legacy offerToReceive* options would turn
          // the audio transceiver inactive, which the server stops for good.
          const offer = await peer.createOffer();
          offer.sdp = stereoOffer(offer.sdp ?? '');

          await peer.setLocalDescription(offer);

          sendMsg('video-offer', JSON.stringify(peer.localDescription));
        } catch (error) {
          videoOfferSent.current = false;
          console.error('Video negotiation failed:', error);
          scheduleReconnect('creating or sending the offer', error);
        }
      };

      peer.onconnectionstatechange = () => {
        if (disposed || peer !== video) {
          return;
        }

        if (peer.connectionState === 'failed') {
          signalingLost();
          scheduleReconnect('peer connection state is failed', undefined, true);
        } else if (peer.connectionState === 'connected') {
          cancelReconnect();
          notificationApi.destroy(WEBRTC_CONNECTION_FAILED_NOTIFICATION_KEY);
        }
      };

      peer.ontrack = (event) => {
        if (videoElement && event.track.kind === 'video') {
          videoElement.srcObject = new MediaStream([event.track]);
        } else if (event.track.kind === 'audio') {
          audioChannel?.setStream(event.streams[0] ?? new MediaStream([event.track]));
        }
      };

      peer.onicecandidate = (event) => {
        if (event.candidate) {
          sendMsg('video-candidate', JSON.stringify(event.candidate));
        }
      };

      const videoTransceiver = peer.addTransceiver('video', { direction: 'recvonly' });
      if (canRecordReceiver()) {
        recording = receiverRecording(
          videoTransceiver.receiver,
          codec,
          () => ({
            width: videoElement?.videoWidth ?? 0,
            height: videoElement?.videoHeight ?? 0
          }),
          () => {
            // At once: the connection may still turn connected, which cancels
            // a scheduled reconnect, while the picture stays frozen.
            if (disposed) return;
            console.error('WebRTC recording transform failed; reconnecting without it');
            setConnectionAttempt((attempt) => attempt + 1);
          }
        );
        if (recording) publishRecordingSource(recording.source);
      }
      // Always offered, so USB audio enabled later needs no renegotiation;
      // the server sends nothing until audio-start.
      audioChannel = createVideoAudioChannel(peer, (event) => sendMsg(event, ''));
      audioTransceiver = peer.addTransceiver('audio', { direction: 'recvonly' });
      recordingAudio = recordAudioReceiver(audioTransceiver.receiver, () => {
        if (!disposed) setConnectionAttempt((attempt) => attempt + 1);
      });
    };

    const handleVideoAnswer = (data: RTCSessionDescriptionInit) => {
      const peer = video;
      if (!peer) {
        return;
      }

      if (peer.signalingState !== 'have-local-offer') {
        videoOfferSent.current = false;
        console.warn(`Video signaling state incorrect for answer: ${peer.signalingState}`);
        return;
      }

      peer
        .setRemoteDescription(new RTCSessionDescription(data))
        .then(() => {
          videoOfferSent.current = false;
          if (peer === video && audioChannel && audioTransceiver?.currentDirection === 'recvonly')
            publishVideoAudio(audioChannel);
          videoIceCandidates.current.forEach((candidate) => {
            peer
              .addIceCandidate(candidate)
              .catch((e) => console.error('Video candidate failed to add:', e.message));
          });
          videoIceCandidates.current = [];
        })
        .catch((error) => {
          console.error('Video answer set failed:', error);
          videoOfferSent.current = false;
          scheduleReconnect('setting the remote description', error);
        });
    };

    const handleVideoCandidate = (data: RTCIceCandidateInit) => {
      const peer = video;
      if (!peer || !data.candidate) {
        return;
      }

      const candidate = new RTCIceCandidate(data);
      if (peer.remoteDescription) {
        peer
          .addIceCandidate(candidate)
          .catch((e) => console.error('Video candidate failed to add:', e.message));
      } else {
        videoIceCandidates.current.push(candidate);
      }
    };

    ws.onopen = () => {
      signalingOpened = true;
      clearTimeout(signalingConnectTimer);
      signalingRecovery.opened(Date.now() - signalingCreatedAt);
      if (disposed) {
        ws.close();
        return;
      }

      heartbeatTimer = setInterval(() => {
        sendMsg('heartbeat', '');
      }, 60 * 1000);
    };

    ws.onerror = (error) => {
      signalingLost();
      scheduleReconnect('the signaling WebSocket returned an error', error);
    };

    ws.onclose = () => {
      signalingLost();
      if (!terminalFailure) {
        scheduleReconnect('the signaling WebSocket closed');
      }
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data as string) as SignalingMessage;

        switch (msg.event) {
          case 'ice-servers': {
            const iceServers = parseSignalingData<RTCIceServer[]>(msg.data);
            startVideo(Array.isArray(iceServers) ? iceServers : []);
            break;
          }
          case 'video-answer': {
            const data = parseSignalingData<RTCSessionDescriptionInit>(msg.data);
            if (data) {
              handleVideoAnswer(data);
            }
            break;
          }
          case 'video-candidate': {
            const data = parseSignalingData<RTCIceCandidateInit>(msg.data);
            if (data) {
              handleVideoCandidate(data);
            }
            break;
          }
          case 'video-error': {
            terminalFailure = true;
            cancelReconnect();
            setIsLoading(false);
            if (msg.data) console.error('WebRTC video stream rejected:', msg.data);
            const retryingJoin = onEncoderConflict(msg.data === 'encoder-reconfigured');
            if (!retryingJoin)
              notificationApi.error({
                key: WEBRTC_CONNECTION_FAILED_NOTIFICATION_KEY,
                message: translationRef.current('screen.encoderError'),
                description: translationRef.current('screen.encoderConflict'),
                placement: 'topRight',
                duration: 0
              });
            stopDiagnostics?.();
            closeAudioChannel();
            video?.close();
            video = null;
            closeRecording();
            ws.close();
            break;
          }
          case 'audio-started':
            audioChannel?.emit('started');
            break;
          case 'audio-stopped':
            audioChannel?.emit('stopped');
            break;
          case 'audio-error':
            audioChannel?.emit('error', msg.data);
            break;
          case 'heartbeat':
            break;
          default:
            console.log('Unhandled event:', msg.event);
        }
      } catch (err) {
        console.error('Message processing error:', err);
      }
    };

    const loadingTimer = setTimeout(() => {
      setIsLoading(false);

      if (!video || video.connectionState !== 'connected') {
        showConnectionFailureNotification();
      }
    }, 5 * 1000);

    const connectionTimeoutTimer = setTimeout(() => {
      if (!video || video.connectionState !== 'connected') {
        scheduleReconnect(
          `connection timed out in state ${video?.connectionState ?? 'not initialized'}`
        );
      }
    }, WEBRTC_CONNECTION_TIMEOUT);

    return () => {
      disposed = true;
      setScreenshotSource(null);
      stopDiagnostics?.();
      cancelReconnect();
      closeAudioChannel();

      if (ws.readyState !== WebSocket.CLOSING && ws.readyState !== WebSocket.CLOSED) {
        ws.close();
      }

      video?.close();
      video = null;
      closeRecording();
      if (videoElement) {
        videoElement.srcObject = null;
      }
      videoOfferSent.current = false;
      videoIceCandidates.current = [];

      if (heartbeatTimer) {
        clearInterval(heartbeatTimer);
      }
      clearTimeout(loadingTimer);
      clearTimeout(connectionTimeoutTimer);
      clearTimeout(signalingConnectTimer);
    };
  }, [connectionAttempt, notificationApi, diagnostics, onEncoderConflict, setScreenshotSource]);

  useEffect(() => {
    return () => {
      notificationApi.destroy(WEBRTC_CONNECTION_FAILED_NOTIFICATION_KEY);
    };
  }, [notificationApi]);

  return (
    <div className="relative h-full min-h-0 w-full min-w-0 overflow-hidden">
      {contextHolder}

      <ScreenViewport>
        <video
          id="screen"
          ref={videoRef}
          className={clsx('block touch-none select-none', mouseStyle)}
          muted
          autoPlay
          playsInline
          controls={false}
          onClick={(e) => {
            e.stopPropagation();
            e.preventDefault();
          }}
          onPlaying={() => {
            setIsLoading(false);
            updateScreenshotSource();
          }}
          onLoadedData={updateScreenshotSource}
          onResize={updateScreenshotSource}
        />
      </ScreenViewport>

      {diagnostics && (
        <output
          id="webrtc-diagnostics"
          ref={diagnosticsRef}
          className="pointer-events-none absolute bottom-1 left-1 max-w-full bg-black/70 px-2 text-xs text-white"
        />
      )}
      {isLoading && (
        <div className="pointer-events-none absolute inset-0 z-50 flex items-center justify-center bg-black/30 backdrop-blur-[2px] transition-all duration-300">
          <Spin size="large" />
        </div>
      )}
    </div>
  );
};
