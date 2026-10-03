// NanoKVM OS transport adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use bytes::Bytes;
use std::io;
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWriteExt},
    net::UnixStream,
};

const MEDIA_MAGIC: &[u8; 4] = b"OKVF";
const MEDIA_VERSION: u8 = 1;
const MEDIA_HEADER_LENGTH: usize = 40;
const MAX_MEDIA_PAYLOAD: usize = 8 << 20;

#[derive(Clone, Debug)]
pub struct Identity {
    pub extension_id: String,
    pub token: String,
}
impl Identity {
    pub fn load() -> io::Result<Self> {
        Ok(Self {
            extension_id: "nanokvm-rustdesk".into(),
            token: String::new(),
        })
    }
}
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
    pub payload: Bytes,
}

pub struct MediaSubscriber {
    stream: UnixStream,
    last_sequence: u64,
}
impl MediaSubscriber {
    pub async fn connect(
        path: &str,
        _identity: &Identity,
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
    pub async fn read_frame(&mut self) -> io::Result<MediaFrame> {
        let frame = read_media_frame(&mut self.stream).await?;
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
}
async fn read_media_frame<R>(reader: &mut R) -> io::Result<MediaFrame>
where
    R: AsyncRead + Unpin,
{
    read_media_frame_prefixed(reader, None).await
}

async fn read_media_frame_prefixed<R>(reader: &mut R, prefix: Option<u8>) -> io::Result<MediaFrame>
where
    R: AsyncRead + Unpin,
{
    let mut header = [0_u8; MEDIA_HEADER_LENGTH];
    let offset = if let Some(prefix) = prefix {
        header[0] = prefix;
        1
    } else {
        0
    };
    reader.read_exact(&mut header[offset..]).await?;
    if &header[..4] != MEDIA_MAGIC || header[4] != MEDIA_VERSION {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid OneKVM media frame header",
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
                format!("unsupported OneKVM codec id {value}"),
            ));
        }
    };
    let payload_length = u32::from_be_bytes(header[32..36].try_into().unwrap()) as usize;
    if payload_length > MAX_MEDIA_PAYLOAD {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "OneKVM media frame is too large",
        ));
    }
    let mut payload = vec![0; payload_length];
    reader.read_exact(&mut payload).await?;
    Ok(MediaFrame {
        sequence: u64::from_be_bytes(header[8..16].try_into().unwrap()),
        pts_usec: i64::from_be_bytes(header[16..24].try_into().unwrap()),
        duration_usec: u32::from_be_bytes(header[24..28].try_into().unwrap()),
        width: u16::from_be_bytes(header[28..30].try_into().unwrap()),
        height: u16::from_be_bytes(header[30..32].try_into().unwrap()),
        codec,
        keyframe: header[6] & 1 != 0,
        payload: payload.into(),
    })
}

pub async fn query_video_info(socket_path: &str, identity: &Identity) -> io::Result<VideoInfo> {
    let mut subscriber = MediaSubscriber::connect(socket_path, identity, "info", None).await?;
    let frame = subscriber.read_frame().await?;
    if !frame.payload.is_empty()
        || frame.width == 0
        || frame.height == 0
        || frame.duration_usec == 0
    {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid OneKVM video metadata",
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

#[derive(Clone, Debug)]
pub struct HidClient {
    socket_path: String,
    identity: Identity,
    session: String,
}

impl HidClient {
    pub fn new(socket_path: String, identity: Identity) -> Self {
        Self {
            socket_path,
            identity,
            session: format!("{:032x}", rand::random::<u128>()),
        }
    }

    pub async fn prepare(&self) -> io::Result<()> {
        tokio::time::timeout(
            std::time::Duration::from_secs(50),
            self.post_inner("/api/hid/prepare", &serde_json::json!({})),
        )
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "NanoKVM USB preparation timed out"))?
    }

    pub async fn keyboard(&self, modifiers: u8, keys: &[u8]) -> io::Result<()> {
        self.post(
            "/api/hid/keyboard",
            &serde_json::json!({"modifiers": modifiers, "keys": keys}),
        )
        .await
    }

    pub async fn mouse(&self, buttons: u8, x: i8, y: i8, wheel: i8) -> io::Result<()> {
        self.post(
            "/api/hid/mouse",
            &serde_json::json!({"buttons": buttons, "x": x, "y": y, "wheel": wheel}),
        )
        .await
    }

    pub async fn absolute_mouse(&self, buttons: u16, x: u16, y: u16) -> io::Result<()> {
        self.absolute_mouse_wheel(buttons, x, y, 0).await
    }
    pub async fn absolute_mouse_wheel(
        &self,
        buttons: u16,
        x: u16,
        y: u16,
        wheel: i8,
    ) -> io::Result<()> {
        self.post(
            "/api/hid/mouse/absolute",
            &serde_json::json!({"buttons": buttons, "x": x, "y": y, "wheel": wheel}),
        )
        .await
    }

    pub async fn heartbeat(&self) -> io::Result<()> {
        self.post("/api/hid/heartbeat", &serde_json::json!({}))
            .await
    }
    pub async fn close(&self) {
        let _ = self.post("/api/hid/close", &serde_json::json!({})).await;
    }
    async fn post(&self, path: &str, value: &serde_json::Value) -> io::Result<()> {
        tokio::time::timeout(
            std::time::Duration::from_secs(3),
            self.post_inner(path, value),
        )
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "NanoKVM HID request timed out"))?
    }
    async fn post_inner(&self, path: &str, value: &serde_json::Value) -> io::Result<()> {
        let body = serde_json::to_vec(value).map_err(io::Error::other)?;
        let mut stream = UnixStream::connect(&self.socket_path).await?;
        let headers = format!(
            "POST {path} HTTP/1.1\r\nHost: unix\r\nContent-Type: application/json\r\nContent-Length: {}\r\nAuthorization: Bearer {}\r\nX-OneKVM-Extension: {}\r\nX-NanoKVM-Session: {}\r\nConnection: close\r\n\r\n",
            body.len(), self.identity.token, self.identity.extension_id, self.session
        );
        stream.write_all(headers.as_bytes()).await?;
        stream.write_all(&body).await?;
        // Content-Length terminates the request body. Sending a write-half EOF
        // here cancels Go net/http's request context before its HID write.
        // Connection: close lets the server close after sending its response.
        let mut response = Vec::with_capacity(512);
        (&mut stream).take(4096).read_to_end(&mut response).await?;
        let status_line = response
            .split(|value| *value == b'\n')
            .next()
            .unwrap_or_default();
        if status_line.starts_with(b"HTTP/1.1 2") || status_line.starts_with(b"HTTP/1.0 2") {
            return Ok(());
        }
        Err(io::Error::other(format!(
            "OneKVM HID request failed: {}",
            String::from_utf8_lossy(status_line).trim()
        )))
    }
}

#[cfg(test)]
pub(crate) async fn read_test_http_request(stream: &mut UnixStream) -> Vec<u8> {
    let mut request = Vec::new();
    while !request.ends_with(b"\r\n\r\n") {
        assert!(request.len() < 8192, "oversized test HTTP headers");
        request.push(stream.read_u8().await.unwrap());
    }
    let headers = String::from_utf8_lossy(&request);
    let length: usize = headers
        .lines()
        .find_map(|line| {
            let (name, value) = line.split_once(':')?;
            name.eq_ignore_ascii_case("Content-Length")
                .then(|| value.trim().parse().unwrap())
        })
        .unwrap();
    let offset = request.len();
    request.resize(offset + length, 0);
    stream.read_exact(&mut request[offset..]).await.unwrap();
    request
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{input::InputState, protocol::MouseEvent};
    use tokio::net::UnixListener;

    #[tokio::test]
    async fn hid_http_keeps_write_half_open_until_server_response() {
        let dir = std::env::temp_dir().join(format!("nk-rd-http-{:x}", rand::random::<u64>()));
        std::fs::create_dir(&dir).unwrap();
        let path = dir.join("hid.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let server = tokio::spawn(async move {
            let (mut stream, _) = listener.accept().await.unwrap();
            let request = read_test_http_request(&mut stream).await;
            assert!(request.starts_with(b"POST /api/hid/mouse "));
            // Go net/http cancels r.Context() on a client write-half EOF.
            // An HTTP client must wait for the response without sending it.
            assert!(
                tokio::time::timeout(std::time::Duration::from_millis(20), stream.read_u8())
                    .await
                    .is_err(),
                "HID client sent EOF before receiving its HTTP response"
            );
            stream
                .write_all(b"HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
                .await
                .unwrap();
        });
        let hid = HidClient::new(
            path.to_string_lossy().into_owned(),
            Identity::load().unwrap(),
        );
        let result = hid.mouse(0, 1, 0, 0).await;
        server.await.unwrap();
        std::fs::remove_dir_all(dir).unwrap();
        result.unwrap();
    }

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
            let identity = Identity::load().unwrap();
            let info = query_video_info(path.to_str().unwrap(), &identity)
                .await
                .unwrap();
            assert_eq!(info.codec, codec);
            let profile = VideoProfile { codec: info.codec };
            let mut encoded = MediaSubscriber::connect(
                path.to_str().unwrap(),
                &identity,
                "encoded",
                Some(&profile),
            )
            .await
            .unwrap();
            assert_eq!(encoded.read_frame().await.unwrap().codec, codec);
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
        let error = read_media_frame(&mut reader).await.unwrap_err();
        assert!(error.to_string().contains("selected encoder is H.265"));
    }

    #[tokio::test]
    async fn absolute_scroll_preserves_position_and_buttons() {
        let dir = std::env::temp_dir().join(format!("nk-rd-{:x}", rand::random::<u64>()));
        std::fs::create_dir(&dir).unwrap();
        let path = dir.join("hid.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let server = tokio::spawn(async move {
            for index in 0..3 {
                let (mut stream, _) = listener.accept().await.unwrap();
                let request = read_test_http_request(&mut stream).await;
                let text = String::from_utf8(request).unwrap();
                assert!(text.starts_with("POST /api/hid/mouse/absolute "));
                assert!(text.contains("X-NanoKVM-Session: "));
                let body: serde_json::Value =
                    serde_json::from_str(text.split("\r\n\r\n").nth(1).unwrap()).unwrap();
                assert_eq!(body["x"], 32767);
                assert_eq!(body["y"], 32767);
                if index == 2 {
                    assert_eq!(body["buttons"], 1);
                    assert_eq!(body["wheel"], -1);
                }
                stream
                    .write_all(b"HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")
                    .await
                    .unwrap();
            }
        });
        let hid = HidClient::new(
            path.to_string_lossy().into_owned(),
            Identity::load().unwrap(),
        );
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

    #[tokio::test]
    async fn oversized_bridge_error_is_rejected() {
        let (mut writer, mut reader) = tokio::io::duplex(128);
        let mut header = [0_u8; 40];
        header[..4].copy_from_slice(b"OKVF");
        header[4] = 1;
        header[32..36].copy_from_slice(&2049_u32.to_be_bytes());
        writer.write_all(&header).await.unwrap();
        assert_eq!(
            read_media_frame(&mut reader).await.unwrap_err().kind(),
            io::ErrorKind::InvalidData
        );
    }
}
