// Copy received Opus packets before decoding. Playback keeps the original frames.
export type ReceivedAudio = {
  recording: number;
  rtpTimestamp: number;
  receivedAt: number;
  data: ArrayBuffer;
};
let recording = 0;
self.onmessage = (event: MessageEvent<{ recording: number }>) => {
  recording = event.data.recording;
};
self.onrtctransform = (event: RTCTransformEvent) => {
  void event.transformer.readable
    .pipeThrough(
      new TransformStream<RTCEncodedAudioFrame, RTCEncodedAudioFrame>({
        transform(frame, controller) {
          if (recording && frame.data.byteLength) {
            const data = frame.data.slice(0);
            const copy: ReceivedAudio = {
              recording,
              rtpTimestamp: frame.getMetadata().rtpTimestamp ?? frame.timestamp,
              receivedAt: performance.timeOrigin + performance.now(),
              data
            };
            self.postMessage(copy, [data]);
          }
          controller.enqueue(frame);
        }
      })
    )
    .pipeTo(event.transformer.writable)
    .catch(() => self.postMessage({ type: 'transform-error' }));
};
