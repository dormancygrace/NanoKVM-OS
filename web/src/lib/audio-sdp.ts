// Explicit stereo receive preference for the separate audio-only connection.
export function stereoOffer(sdp: string): string {
  const eol = sdp.includes('\r\n') ? '\r\n' : '\n';
  const lines = sdp.split(eol);
  const opus = lines.find((line) => /^a=rtpmap:\d+ opus\/48000\/2$/i.test(line));
  if (!opus) return sdp;
  const payload = opus.split(':')[1].split(' ')[0];
  const prefix = `a=fmtp:${payload} `;
  const index = lines.findIndex((line) => line.startsWith(prefix));
  const params =
    index < 0
      ? []
      : lines[index]
          .slice(prefix.length)
          .split(';')
          .filter((param) => !/^(stereo|sprop-stereo|maxaveragebitrate)=/.test(param.trim()));
  const line =
    prefix + [...params, 'stereo=1', 'sprop-stereo=1', 'maxaveragebitrate=192000'].join(';');
  if (index < 0) lines.splice(lines.indexOf(opus) + 1, 0, line);
  else lines[index] = line;
  return lines.join(eol);
}
