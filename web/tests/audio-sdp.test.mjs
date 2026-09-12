import assert from 'node:assert/strict';
import test from 'node:test';
import { stereoOffer } from '../src/lib/audio-sdp.ts';
test('stereo preference replaces conflicting values and preserves FEC',()=>{
  const input='m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=rtpmap:111 opus/48000/2\r\na=fmtp:111 minptime=10;stereo=0;useinbandfec=1\r\n';
  const result=stereoOffer(input);
  assert.match(result,/stereo=1;sprop-stereo=1;maxaveragebitrate=192000/);
  assert.match(result,/useinbandfec=1/);
  assert.equal(result.includes('stereo=0'),false);
  assert.equal(stereoOffer(result),result);
});
test('adds missing fmtp without affecting other codecs',()=>{
  const input='a=rtpmap:109 opus/48000/2\na=rtpmap:0 PCMU/8000\n';
  assert.match(stereoOffer(input),/a=fmtp:109 stereo=1/);
  assert.match(stereoOffer(input),/a=rtpmap:0 PCMU\/8000/);
  assert.equal(stereoOffer('a=rtpmap:0 PCMU/8000\n'),'a=rtpmap:0 PCMU/8000\n');
});
