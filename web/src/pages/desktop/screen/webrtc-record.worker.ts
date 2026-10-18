// The WebRTC encoded transform of the video receiver: every received frame
// goes on to the decoder unchanged; while a recording runs, a copy goes to
// the page as well.

export type ReceivedFrame = {
  recording: number;
  key: boolean;
  rtpTimestamp: number;
  width: number;
  height: number;
  data: ArrayBuffer;
};

// The recording that receives copies; 0 for none.
let recording = 0;
let transformer: RTCRtpScriptTransformer | null = null;

// Older servers ignore the request; the recording then starts at the next
// regular keyframe.
const requestKeyframe = () => transformer?.sendKeyFrameRequest().catch(() => {});

// Each message that names a recording also asks for a keyframe.
self.onmessage = (event: MessageEvent<{ recording: number }>) => {
  recording = event.data.recording;
  if (recording) void requestKeyframe();
};

self.onrtctransform = (event: RTCTransformEvent) => {
  transformer = event.transformer;
  if (recording) void requestKeyframe();
  void transformer.readable
    .pipeThrough(
      new TransformStream<RTCEncodedVideoFrame, RTCEncodedVideoFrame>({
        transform(frame, controller) {
          // Chrome also passes frames without data, typed 'empty'.
          if (recording && (frame.type as string) !== 'empty') {
            const metadata = frame.getMetadata();
            const data = frame.data.slice(0);
            const copy: ReceivedFrame = {
              recording,
              key: frame.type === 'key',
              rtpTimestamp: metadata.rtpTimestamp ?? frame.timestamp,
              width: metadata.width ?? 0,
              height: metadata.height ?? 0,
              data
            };
            self.postMessage(copy, [data]);
          }
          controller.enqueue(frame);
        }
      })
    )
    .pipeTo(transformer.writable)
    .catch(() => {});
};
