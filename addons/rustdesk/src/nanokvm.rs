// NanoKVM OS transport adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use bytes::BufMut;
use std::{io, time::Duration};
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWriteExt},
    net::UnixStream,
    time,
};

const MEDIA_MAGIC: &[u8; 4] = b"OKVF";
const MEDIA_VERSION: u8 = 1;
const MEDIA_HEADER_LENGTH: usize = 40;
const MAX_MEDIA_PAYLOAD: usize = 8 << 20;

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct VideoInfo {
    pub codec: Codec,
    pub width: u16,
    pub height: u16,
    pub fps: u32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Codec {
    H264,
    H265,
    Mjpeg,
}

#[derive(Clone, Debug)]
pub struct VideoProfile {
    pub codec: Codec,
}

#[derive(Debug)]
pub struct MediaFrame {
    pub sequence: u64,
    pub pts_usec: i64,
    pub duration_usec: u32,
    pub width: u16,
    pub height: u16,
    pub codec: Codec,
    pub keyframe: bool,
    pub payload: MediaPayload,
}

/// A frame's bytes with room around them: the session's writer puts the
/// protocol's headers and trailer around the payload and sends the buffer as
/// it is. Reads as the payload.
#[derive(Debug)]
pub struct MediaPayload(Vec<u8>);
impl MediaPayload {
    /// Unused bytes before the payload.
    pub const FRONT: usize = 64;
    /// Capacity kept after the payload.
    pub const BACK: usize = 16;

    // Reads `length` bytes into `buffer`, an earlier frame's (see `spare`)
    // or an empty one, without filling it first.
    async fn read<R>(reader: &mut R, length: usize, mut buffer: Vec<u8>) -> io::Result<Self>
    where
        R: AsyncRead + Unpin,
    {
        buffer.clear();
        buffer.reserve(Self::FRONT + length + Self::BACK);
        buffer.resize(Self::FRONT, 0);
        let mut payload = (&mut buffer).limit(length);
        while payload.has_remaining_mut() {
            if reader.read_buf(&mut payload).await? == 0 {
                return Err(io::ErrorKind::UnexpectedEof.into());
            }
        }
        Ok(Self(buffer))
    }

    /// The buffer: the payload starts at FRONT and BACK more bytes fit
    /// after it without a new allocation.
    pub fn into_buffer(self) -> Vec<u8> {
        self.0
    }

    /// What a later frame is read into once this frame's buffer is done
    /// with: the buffer, or nothing when it grew for a large keyframe, which
    /// is freed at once.
    pub fn spare(buffer: Vec<u8>) -> Vec<u8> {
        if buffer.capacity() > crate::framing::MAX_RETAINED_BUFFER {
            return Vec::new();
        }
        buffer
    }
}
impl std::ops::Deref for MediaPayload {
    type Target = [u8];
    fn deref(&self) -> &[u8] {
        &self.0[Self::FRONT..]
    }
}
#[cfg(test)]
impl From<Vec<u8>> for MediaPayload {
    fn from(payload: Vec<u8>) -> Self {
        let mut buffer = vec![0; Self::FRONT];
        buffer.extend_from_slice(&payload);
        buffer.reserve(Self::BACK);
        Self(buffer)
    }
}

pub struct MediaSubscriber {
    stream: UnixStream,
    last_sequence: u64,
}
impl MediaSubscriber {
    pub async fn connect(
        path: &str,
        video: &str,
        profile: Option<&VideoProfile>,
    ) -> io::Result<Self> {
        let mut stream = UnixStream::connect(path).await?;
        let mut request = serde_json::to_vec(&serde_json::json!({
            "version": 1, "video": video,
            "codec": profile.map(|p| codec_name(p.codec)),
        }))?;
        request.push(b'\n');
        stream.write_all(&request).await?;
        Ok(Self {
            stream,
            last_sequence: 0,
        })
    }
    /// Reads the next frame into `buffer` (see MediaPayload::into_buffer).
    pub async fn read_frame(&mut self, buffer: Vec<u8>) -> io::Result<MediaFrame> {
        let frame = read_media_frame(&mut self.stream, buffer).await?;
        if self.last_sequence != 0 && frame.sequence != self.last_sequence + 1 && !frame.keyframe {
            self.request_key_frame().await?;
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "media sequence gap",
            ));
        }
        self.last_sequence = frame.sequence;
        Ok(frame)
    }
    pub async fn request_key_frame(&mut self) -> io::Result<()> {
        self.stream.write_all(b"{\"request\":\"keyframe\"}\n").await
    }
    /// The encoder is shared: it runs at this rate at most while this
    /// subscriber is there.
    pub async fn request_fps(&mut self, fps: u32) -> io::Result<()> {
        let line = format!("{{\"request\":\"fps\",\"fps\":{fps}}}\n");
        self.stream.write_all(line.as_bytes()).await
    }
}
async fn read_media_frame<R>(reader: &mut R, buffer: Vec<u8>) -> io::Result<MediaFrame>
where
    R: AsyncRead + Unpin,
{
    let mut header = [0_u8; MEDIA_HEADER_LENGTH];
    reader.read_exact(&mut header).await?;
    if &header[..4] != MEDIA_MAGIC || header[4] != MEDIA_VERSION {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid media frame header",
        ));
    }
    if header[5] == 0 {
        let length = u32::from_be_bytes(header[32..36].try_into().unwrap()) as usize;
        if length > 2048 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "invalid bridge error length",
            ));
        }
        let mut message = vec![0; length];
        reader.read_exact(&mut message).await?;
        return Err(io::Error::other(
            String::from_utf8_lossy(&message).into_owned(),
        ));
    }
    let codec = match header[5] {
        1 => Codec::H264,
        2 => Codec::H265,
        3 => Codec::Mjpeg,
        value => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!("unsupported codec id {value}"),
            ));
        }
    };
    let payload_length = u32::from_be_bytes(header[32..36].try_into().unwrap()) as usize;
    if payload_length > MAX_MEDIA_PAYLOAD {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "media frame is too large",
        ));
    }
    let payload = MediaPayload::read(reader, payload_length, buffer).await?;
    Ok(MediaFrame {
        sequence: u64::from_be_bytes(header[8..16].try_into().unwrap()),
        pts_usec: i64::from_be_bytes(header[16..24].try_into().unwrap()),
        duration_usec: u32::from_be_bytes(header[24..28].try_into().unwrap()),
        width: u16::from_be_bytes(header[28..30].try_into().unwrap()),
        height: u16::from_be_bytes(header[30..32].try_into().unwrap()),
        codec,
        keyframe: header[6] & 1 != 0,
        payload,
    })
}

pub async fn query_video_info(socket_path: &str) -> io::Result<VideoInfo> {
    let mut subscriber = MediaSubscriber::connect(socket_path, "info", None).await?;
    let frame = subscriber.read_frame(Vec::new()).await?;
    if !frame.payload.is_empty()
        || frame.width == 0
        || frame.height == 0
        || frame.duration_usec == 0
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid video metadata",
        ));
    }
    Ok(VideoInfo {
        codec: frame.codec,
        width: frame.width,
        height: frame.height,
        fps: (1_000_000_u32 / frame.duration_usec).max(1),
    })
}

fn codec_name(codec: Codec) -> &'static str {
    match codec {
        Codec::H264 => "h264",
        Codec::H265 => "h265",
        Codec::Mjpeg => "mjpeg",
    }
}

// OKHC v1, the input and control channel to the app bridge. Its layout is
// described at server/service/rustdesk/control.go; a test here and one there
// hold the same bytes.
const CONTROL_MAGIC: &[u8; 4] = b"OKHC";
const CONTROL_VERSION: u8 = 1;
const CONTROL_HELLO_LENGTH: usize = 24;
const RECORD_LENGTH: usize = 8;
const MAX_RECORDS: usize = 8;
const RECORD_KEYBOARD: u8 = 1;
const RECORD_RELATIVE: u8 = 2;
const RECORD_ABSOLUTE: u8 = 3;
const RECORD_HEARTBEAT: u8 = 4;
const RECORD_CLOSE: u8 = 5;
const RECORD_PREPARE: u8 = 6;
const STATUS_OK: u8 = 0;
const STATUS_LEASE_HELD: u8 = 1;
const REQUEST_TIMEOUT: Duration = Duration::from_secs(3);
const PREPARE_TIMEOUT: Duration = Duration::from_secs(50);

// One frame, a count and its records, built in place: a report allocates
// nothing and goes out in one write.
struct ControlFrame([u8; 1 + RECORD_LENGTH * MAX_RECORDS]);
impl ControlFrame {
    fn new() -> Self {
        Self([0; 1 + RECORD_LENGTH * MAX_RECORDS])
    }
    fn push(&mut self, kind: u8, payload: &[u8]) {
        let start = 1 + RECORD_LENGTH * usize::from(self.0[0]);
        self.0[start] = kind;
        self.0[start + 1..start + 1 + payload.len()].copy_from_slice(payload);
        self.0[0] += 1;
    }
    fn keyboard(&mut self, modifiers: u8, keys: [u8; 6]) {
        let [a, b, c, d, e, f] = keys;
        self.push(RECORD_KEYBOARD, &[modifiers, a, b, c, d, e, f]);
    }
    fn relative(&mut self, buttons: u8, x: i8, y: i8, wheel: i8) {
        self.push(RECORD_RELATIVE, &[buttons, x as u8, y as u8, wheel as u8]);
    }
    fn absolute(&mut self, buttons: u8, x: u16, y: u16, wheel: i8) {
        let ([x0, x1], [y0, y1]) = (x.to_le_bytes(), y.to_le_bytes());
        self.push(RECORD_ABSOLUTE, &[buttons, x0, x1, y0, y1, wheel as u8]);
    }
    fn as_bytes(&self) -> &[u8] {
        &self.0[..1 + RECORD_LENGTH * usize::from(self.0[0])]
    }
}

fn control_hello(session: [u8; 16]) -> [u8; CONTROL_HELLO_LENGTH] {
    let mut hello = [0; CONTROL_HELLO_LENGTH];
    hello[..4].copy_from_slice(CONTROL_MAGIC);
    hello[4] = CONTROL_VERSION;
    hello[8..].copy_from_slice(&session);
    hello
}

async fn transfer(stream: &mut UnixStream, frame: &[u8]) -> io::Result<u8> {
    stream.write_all(frame).await?;
    stream.read_u8().await
}

/// The input of one RustDesk session at the app bridge, on one connection
/// that is opened with the first frame and kept.
#[derive(Debug)]
pub struct HidClient {
    socket_path: String,
    session: [u8; 16],
    stream: Option<UnixStream>,
}

impl HidClient {
    pub fn new(socket_path: String) -> Self {
        Self {
            socket_path,
            session: rand::random::<u128>().to_be_bytes(),
            stream: None,
        }
    }

    pub async fn prepare(&mut self) -> io::Result<()> {
        let mut frame = ControlFrame::new();
        frame.push(RECORD_PREPARE, &[]);
        self.exchange(&frame, PREPARE_TIMEOUT).await
    }

    pub async fn keyboard(&mut self, modifiers: u8, keys: [u8; 6]) -> io::Result<()> {
        let mut frame = ControlFrame::new();
        frame.keyboard(modifiers, keys);
        self.exchange(&frame, REQUEST_TIMEOUT).await
    }

    pub async fn mouse(&mut self, buttons: u8, x: i8, y: i8, wheel: i8) -> io::Result<()> {
        let mut frame = ControlFrame::new();
        frame.relative(buttons, x, y, wheel);
        self.exchange(&frame, REQUEST_TIMEOUT).await
    }

    /// Relative movement in steps, one report each, all in one frame.
    pub async fn relative_moves(&mut self, buttons: u8, moves: &[(i8, i8)]) -> io::Result<()> {
        for moves in moves.chunks(MAX_RECORDS) {
            let mut frame = ControlFrame::new();
            for &(x, y) in moves {
                frame.relative(buttons, x, y, 0);
            }
            self.exchange(&frame, REQUEST_TIMEOUT).await?;
        }
        Ok(())
    }

    pub async fn absolute_mouse(&mut self, buttons: u16, x: u16, y: u16) -> io::Result<()> {
        self.absolute_mouse_wheel(buttons, x, y, 0).await
    }
    pub async fn absolute_mouse_wheel(
        &mut self,
        buttons: u16,
        x: u16,
        y: u16,
        wheel: i8,
    ) -> io::Result<()> {
        // The USB report has eight buttons; the bridge refused more as well.
        let buttons = u8::try_from(buttons)
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "unknown mouse button"))?;
        let mut frame = ControlFrame::new();
        frame.absolute(buttons, x, y, wheel);
        self.exchange(&frame, REQUEST_TIMEOUT).await
    }

    pub async fn heartbeat(&mut self) -> io::Result<()> {
        let mut frame = ControlFrame::new();
        frame.push(RECORD_HEARTBEAT, &[]);
        self.exchange(&frame, REQUEST_TIMEOUT).await
    }
    pub async fn close(&mut self) {
        let mut frame = ControlFrame::new();
        frame.push(RECORD_CLOSE, &[]);
        let _ = self.exchange(&frame, REQUEST_TIMEOUT).await;
    }

    // Sends one frame and reads its status; `limit` covers all of it, a new
    // connection included.
    async fn exchange(&mut self, frame: &ControlFrame, limit: Duration) -> io::Result<()> {
        let status = time::timeout(limit, self.round_trip(frame.as_bytes()))
            .await
            .map_err(|_| {
                io::Error::new(io::ErrorKind::TimedOut, "NanoKVM HID request timed out")
            })??;
        match status {
            STATUS_OK => Ok(()),
            // Another session holds input control. That may end soon, unlike
            // the other failures, so it gets its own kind.
            STATUS_LEASE_HELD => Err(io::Error::new(
                io::ErrorKind::ResourceBusy,
                "another session holds input control",
            )),
            status => Err(io::Error::other(format!(
                "HID request failed: status {status}"
            ))),
        }
    }

    // The connection is taken out for the frame and put back only with its
    // answer. One that failed, ran out of time or was abandoned half-way is
    // closed with it, so a late answer is never read as the next frame's.
    async fn round_trip(&mut self, frame: &[u8]) -> io::Result<u8> {
        if let Some(mut stream) = self.stream.take() {
            if let Ok(status) = transfer(&mut stream, frame).await {
                self.stream = Some(stream);
                return Ok(status);
            }
            // A kept connection breaks when the app stops, which also releases
            // what the session held, or is killed, which releases nothing.
            // The frame goes once more to the app that started since, as a
            // request on its own connection always did. Keyboard and absolute
            // reports carry the whole state, so a repeated one changes
            // nothing and the first one after a killed app sets it right; a
            // relative frame the old app still wrote moves the pointer twice.
        }
        let mut stream = UnixStream::connect(&self.socket_path).await?;
        stream.write_all(&control_hello(self.session)).await?;
        let status = transfer(&mut stream, frame).await?;
        self.stream = Some(stream);
        Ok(status)
    }
}

// The session id and the records of each frame the test bridge receives.
#[cfg(test)]
pub(crate) async fn read_test_control_hello(stream: &mut UnixStream) -> [u8; 16] {
    let mut hello = [0; CONTROL_HELLO_LENGTH];
    stream.read_exact(&mut hello).await.unwrap();
    assert_eq!(hello[..8], control_hello([0; 16])[..8]);
    hello[8..].try_into().unwrap()
}
#[cfg(test)]
pub(crate) async fn read_test_control_frame(stream: &mut UnixStream) -> io::Result<Vec<u8>> {
    let count = usize::from(stream.read_u8().await?);
    assert!((1..=MAX_RECORDS).contains(&count));
    let mut records = vec![0; RECORD_LENGTH * count];
    stream.read_exact(&mut records).await?;
    Ok(records)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{input::InputState, protocol::MouseEvent};
    use tokio::net::UnixListener;

    #[tokio::test]
    async fn metadata_uses_the_device_codec_and_stream_pins_it() {
        for (codec, native) in [(Codec::H264, 1), (Codec::H265, 2)] {
            let dir = std::env::temp_dir().join(format!("nk-rd-codec-{:x}", rand::random::<u64>()));
            std::fs::create_dir(&dir).unwrap();
            let path = dir.join("media.sock");
            let listener = UnixListener::bind(&path).unwrap();
            let server = tokio::spawn(async move {
                for command in ["info", "encoded"] {
                    let (mut stream, _) = listener.accept().await.unwrap();
                    let mut line = Vec::new();
                    loop {
                        let byte = stream.read_u8().await.unwrap();
                        if byte == b'\n' {
                            break;
                        }
                        line.push(byte);
                    }
                    let request: serde_json::Value = serde_json::from_slice(&line).unwrap();
                    assert_eq!(request["video"], command);
                    if command == "info" {
                        assert!(request["codec"].is_null());
                    } else {
                        assert_eq!(request["codec"], codec_name(codec));
                    }
                    let mut header = [0; 40];
                    header[..4].copy_from_slice(b"OKVF");
                    header[4] = 1;
                    header[5] = native;
                    header[24..28].copy_from_slice(&16_667u32.to_be_bytes());
                    header[28..30].copy_from_slice(&1920u16.to_be_bytes());
                    header[30..32].copy_from_slice(&1080u16.to_be_bytes());
                    stream.write_all(&header).await.unwrap();
                }
            });
            let info = query_video_info(path.to_str().unwrap()).await.unwrap();
            assert_eq!(info.codec, codec);
            let profile = VideoProfile { codec: info.codec };
            let mut encoded =
                MediaSubscriber::connect(path.to_str().unwrap(), "encoded", Some(&profile))
                    .await
                    .unwrap();
            assert_eq!(encoded.read_frame(Vec::new()).await.unwrap().codec, codec);
            server.await.unwrap();
            std::fs::remove_dir_all(dir).unwrap();
        }
    }

    #[tokio::test]
    async fn bridge_error_is_reported_without_codec_guessing() {
        let (mut writer, mut reader) = tokio::io::duplex(4096);
        let message = b"selected encoder is H.265";
        let mut header = [0_u8; 40];
        header[..4].copy_from_slice(b"OKVF");
        header[4] = 1;
        header[32..36].copy_from_slice(&(message.len() as u32).to_be_bytes());
        writer.write_all(&header).await.unwrap();
        writer.write_all(message).await.unwrap();
        let error = read_media_frame(&mut reader, Vec::new()).await.unwrap_err();
        assert!(error.to_string().contains("selected encoder is H.265"));
    }

    #[tokio::test]
    async fn absolute_scroll_preserves_position_and_buttons() {
        let dir = std::env::temp_dir().join(format!("nk-rd-{:x}", rand::random::<u64>()));
        std::fs::create_dir(&dir).unwrap();
        let path = dir.join("hid.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            read_test_control_hello(&mut stream).await;
            // Position 32767, 32767 each time; the button, then the wheel.
            for (buttons, wheel) in [(0, 0), (1, 0), (1, 0xff)] {
                assert_eq!(
                    read_test_control_frame(&mut stream).await.unwrap(),
                    [3, buttons, 0xff, 0x7f, 0xff, 0x7f, wheel, 0]
                );
                stream.write_all(&[0]).await.unwrap();
            }
        });
        let hid = HidClient::new(path.to_string_lossy().into_owned());
        let mut input = InputState::new(hid, 1440, 2560);
        input
            .handle_mouse(MouseEvent {
                mask: 0,
                x: 1439,
                y: 2559,
                ..Default::default()
            })
            .await
            .unwrap();
        input
            .handle_mouse(MouseEvent {
                mask: 9,
                ..Default::default()
            })
            .await
            .unwrap();
        input
            .handle_mouse(MouseEvent {
                mask: 3,
                y: 1,
                ..Default::default()
            })
            .await
            .unwrap();
        server.await.unwrap();
        std::fs::remove_file(path).unwrap();
        std::fs::remove_dir(dir).unwrap();
    }

    // The bytes of TestControlChannel in server/service/rustdesk/control_test.go.
    #[test]
    fn control_channel_bytes_match_the_bridge() {
        let session = [
            0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd,
            0xee, 0xff,
        ];
        let mut hello = vec![0x4f, 0x4b, 0x48, 0x43, 0x01, 0x00, 0x00, 0x00];
        hello.extend_from_slice(&session);
        assert_eq!(control_hello(session)[..], hello);
        let mut frame = ControlFrame::new();
        frame.keyboard(0x02, [4, 5, 0, 0, 0, 0]);
        frame.relative(0, -3, 127, -1);
        frame.absolute(1, 0x1234, 32767, 1);
        assert_eq!(
            frame.as_bytes(),
            [
                0x03, 0x01, 0x02, 0x04, 0x05, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0xfd, 0x7f, 0xff,
                0x00, 0x00, 0x00, 0x03, 0x01, 0x34, 0x12, 0xff, 0x7f, 0x01, 0x00,
            ]
        );
    }

    #[tokio::test]
    async fn steps_share_a_write_and_a_refused_or_closed_connection_is_survived() {
        let dir = std::env::temp_dir().join(format!("nk-rd-control-{:x}", rand::random::<u64>()));
        std::fs::create_dir(&dir).unwrap();
        let path = dir.join("control.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let session = read_test_control_hello(&mut stream).await;
            // The three steps of one flush arrive in one read.
            let mut steps = [0; 1 + RECORD_LENGTH * MAX_RECORDS];
            let length = stream.read(&mut steps).await.unwrap();
            assert_eq!((length, steps[0]), (1 + RECORD_LENGTH * 3, 3));
            stream.write_all(&[1]).await.unwrap();
            // The app restarts: the session goes on with a new connection.
            drop(stream);
            let (mut stream, _) = listener.accept().await.unwrap();
            assert_eq!(read_test_control_hello(&mut stream).await, session);
            let frame = read_test_control_frame(&mut stream).await.unwrap();
            assert_eq!(frame[0], 4, "a heartbeat");
            stream.write_all(&[0]).await.unwrap();
        });
        let mut hid = HidClient::new(path.to_string_lossy().into_owned());
        let refused = hid.relative_moves(0, &[(1, 1); 3]).await.unwrap_err();
        assert_eq!(refused.kind(), io::ErrorKind::ResourceBusy);
        hid.heartbeat().await.unwrap();
        server.await.unwrap();
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn oversized_bridge_error_is_rejected() {
        let (mut writer, mut reader) = tokio::io::duplex(128);
        let mut header = [0_u8; 40];
        header[..4].copy_from_slice(b"OKVF");
        header[4] = 1;
        header[32..36].copy_from_slice(&2049_u32.to_be_bytes());
        writer.write_all(&header).await.unwrap();
        assert_eq!(
            read_media_frame(&mut reader, Vec::new())
                .await
                .unwrap_err()
                .kind(),
            io::ErrorKind::InvalidData
        );
    }
}
