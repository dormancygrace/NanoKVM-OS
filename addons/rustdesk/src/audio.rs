// Shared optional USB audio -> canonical RustDesk Opus. SPDX-License-Identifier: AGPL-3.0-only
use bytes::Bytes;
use std::{io, time::Duration};
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWriteExt},
    net::UnixStream,
    sync::watch,
    task::JoinHandle,
    time::{self, Instant},
};

pub const SAMPLE_RATE: u32 = 48_000;
pub const CHANNELS: u32 = 2;
pub const MAX_PACKET: usize = 1275;
const SETUP: Duration = Duration::from_secs(2);
const RETRY: Duration = Duration::from_secs(1);
pub const MAX_AGE: Duration = Duration::from_millis(100);

#[derive(Clone, Default)]
pub struct State {
    pub available: Option<bool>,
    pub generation: u64,
    // One latest frame, independent of network/video backpressure. No sound backlog.
    pub packet: Option<(Instant, Bytes)>,
}
struct Packet {
    available: bool,
    data: Bytes,
}
fn invalid() -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, "invalid shared Opus frame")
}

async fn read_packet<R: AsyncRead + Unpin>(r: &mut R) -> io::Result<Packet> {
    let mut h = [0; 16];
    r.read_exact(&mut h).await?;
    if &h[..4] != b"OKAF" || h[4] != 1 || h[7] != 0 || h[14..16] != [0, 0] {
        return Err(invalid());
    }
    let len = usize::from(u16::from_be_bytes(h[12..14].try_into().unwrap()));
    if len > MAX_PACKET {
        return Err(invalid());
    }
    let available = match h[5] {
        0 if len == 0 && h[6] == 0 && h[8..12] == [0; 4] => false,
        1 if u32::from(h[6]) == CHANNELS
            && u32::from_be_bytes(h[8..12].try_into().unwrap()) == SAMPLE_RATE =>
        {
            true
        }
        _ => return Err(invalid()),
    };
    let mut data = vec![0; len];
    r.read_exact(&mut data).await?;
    Ok(Packet {
        available,
        data: data.into(),
    })
}
async fn connect(path: &str, command: &str) -> io::Result<(UnixStream, bool)> {
    time::timeout(SETUP, async {
        let mut stream = UnixStream::connect(path).await?;
        let mut request = serde_json::to_vec(&serde_json::json!({"version":1,"audio":command}))?;
        request.push(b'\n');
        stream.write_all(&request).await?;
        let format = read_packet(&mut stream).await?;
        if !format.data.is_empty() {
            return Err(invalid());
        }
        Ok((stream, format.available))
    })
    .await
    .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "audio setup timed out"))?
}

pub fn start(
    path: String,
    allowed: bool,
    mut desired: watch::Receiver<bool>,
) -> (JoinHandle<()>, watch::Receiver<State>) {
    let (tx, rx) = watch::channel(State::default());
    let task = tokio::spawn(async move {
        let mut state = State::default();
        if !allowed {
            state.available = Some(false);
            tx.send_replace(state);
            return;
        }
        loop {
            state.packet = None;
            // An info request does not attach to ALSA or start the capture helper.
            let available = tokio::select! {
                result=connect(&path,"info")=>result.map(|(_,yes)|yes).unwrap_or(false),
                result=desired.changed()=>{if result.is_err(){return;}continue;},
            };
            state.available = Some(available);
            tx.send_replace(state.clone());
            if available && *desired.borrow() {
                let connection = tokio::select! {
                    result=connect(&path,"opus")=>result,
                    result=desired.changed()=>{if result.is_err(){return;}continue;},
                };
                if let Ok((mut stream, true)) = connection {
                    state.generation = state.generation.wrapping_add(1).max(1);
                    tx.send_replace(state.clone());
                    loop {
                        // Cancellation drops the whole IPC connection. A partially read
                        // frame is never resumed on a new USB capture or mute generation.
                        let packet = tokio::select! {
                            result=time::timeout(Duration::from_secs(5),read_packet(&mut stream))=>result,
                            _=desired.changed()=>{state.packet=None;tx.send_replace(state.clone());break;},
                        };
                        match packet {
                            Ok(Ok(Packet {
                                available: true,
                                data,
                            })) if !data.is_empty() => {
                                state.packet = Some((Instant::now(), data));
                                tx.send_replace(state.clone());
                            }
                            _ => {
                                state.packet = None;
                                state.available = Some(false);
                                tx.send_replace(state.clone());
                                break;
                            }
                        }
                    }
                } else {
                    state.available = Some(false);
                    tx.send_replace(state.clone());
                }
            }
            tokio::select! {
                _=time::sleep(RETRY)=>{},
                result=desired.changed()=>{if result.is_err(){return;}},
            }
        }
    });
    (task, rx)
}

#[cfg(test)]
mod tests {
    use super::*;
    fn header(size: u16) -> [u8; 16] {
        let mut h = [0; 16];
        h[..4].copy_from_slice(b"OKAF");
        h[4] = 1;
        h[5] = 1;
        h[6] = 2;
        h[8..12].copy_from_slice(&48000u32.to_be_bytes());
        h[12..14].copy_from_slice(&size.to_be_bytes());
        h
    }
    #[tokio::test]
    async fn fragmented_opus_header_preserves_data_and_rejects_bad_format_before_allocation() {
        let (mut write, mut read) = tokio::io::duplex(32);
        let send = tokio::spawn(async move {
            for byte in header(3).into_iter().chain([0xfc, 0xff, 0xfe]) {
                write.write_all(&[byte]).await.unwrap();
                tokio::task::yield_now().await;
            }
        });
        let p = read_packet(&mut read).await.unwrap();
        assert!(p.available);
        assert_eq!(&p.data[..], &[0xfc, 0xff, 0xfe]);
        send.await.unwrap();
        let mut huge = &header(1276)[..];
        assert!(read_packet(&mut huge).await.is_err());
        let mut h = header(0);
        h[6] = 1;
        assert!(read_packet(&mut &h[..]).await.is_err());
        let mut h = header(0);
        h[4] = 2;
        assert!(read_packet(&mut &h[..]).await.is_err());
        let mut short = &header(3)[..];
        assert!(read_packet(&mut short).await.is_err());
    }
    #[tokio::test]
    async fn server_audio_denial_never_connects_or_can_be_overridden_by_client() {
        let (enabled, desired) = watch::channel(true);
        let (task, mut state) = start("/not-used".into(), false, desired);
        state.changed().await.unwrap();
        assert_eq!(state.borrow().available, Some(false));
        task.await.unwrap();
        assert!(enabled.send(false).is_err());
    }

    #[tokio::test]
    async fn mute_releases_capture_and_rebind_resumes_only_the_latest_generation() {
        use tokio::{io::AsyncBufReadExt, net::UnixListener, sync::mpsc};
        let dir = std::env::temp_dir().join(format!(
            "rustdesk-audio-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        std::fs::create_dir(&dir).unwrap();
        let path = dir.join("audio.sock");
        let listener = UnixListener::bind(&path).unwrap();
        let (connections, mut incoming) = mpsc::channel(8);
        let accept = tokio::spawn(async move {
            loop {
                let (mut stream, _) = listener.accept().await.unwrap();
                let mut line = String::new();
                tokio::io::BufReader::new(&mut stream)
                    .read_line(&mut line)
                    .await
                    .unwrap();
                let request: serde_json::Value = serde_json::from_str(&line).unwrap();
                if connections
                    .send((request["audio"].as_str().unwrap().to_owned(), stream))
                    .await
                    .is_err()
                {
                    break;
                }
            }
        });
        async fn next(
            incoming: &mut mpsc::Receiver<(String, UnixStream)>,
            expected: &str,
        ) -> UnixStream {
            let (command, stream) = time::timeout(Duration::from_secs(2), incoming.recv())
                .await
                .unwrap()
                .unwrap();
            assert_eq!(command, expected);
            stream
        }
        let (enabled, desired) = watch::channel(false);
        let (task, mut state) = start(path.to_string_lossy().into_owned(), true, desired);
        let mut info = next(&mut incoming, "info").await;
        info.write_all(&header(0)).await.unwrap();
        state.changed().await.unwrap();
        assert_eq!(state.borrow_and_update().available, Some(true));
        assert!(
            time::timeout(Duration::from_millis(100), incoming.recv())
                .await
                .is_err(),
            "muted session started capture"
        );
        enabled.send(true).unwrap();
        let mut info = next(&mut incoming, "info").await;
        info.write_all(&header(0)).await.unwrap();
        let mut capture = next(&mut incoming, "opus").await;
        capture.write_all(&header(0)).await.unwrap();
        // A blocked network writer observes one recent packet rather than a FIFO backlog.
        for index in 0..100u8 {
            capture.write_all(&header(1)).await.unwrap();
            capture.write_all(&[index]).await.unwrap();
        }
        time::timeout(Duration::from_secs(1), async {
            loop {
                state.changed().await.unwrap();
                let current = state.borrow_and_update().clone();
                if current
                    .packet
                    .as_ref()
                    .is_some_and(|(_, data)| data[0] == 99)
                {
                    break;
                }
            }
        })
        .await
        .unwrap();
        let first_generation = state.borrow().generation;
        enabled.send(false).unwrap();
        assert_eq!(
            time::timeout(Duration::from_secs(1), capture.read_u8())
                .await
                .unwrap()
                .unwrap_err()
                .kind(),
            io::ErrorKind::UnexpectedEof
        );
        // Unavailable USB audio remains observable while the client is muted.
        let mut info = next(&mut incoming, "info").await;
        let mut unavailable = header(0);
        unavailable[5] = 0;
        unavailable[6] = 0;
        unavailable[8..12].fill(0);
        info.write_all(&unavailable).await.unwrap();
        time::timeout(Duration::from_secs(1), async {
            loop {
                state.changed().await.unwrap();
                if state.borrow_and_update().available == Some(false) {
                    break;
                }
            }
        })
        .await
        .unwrap();
        enabled.send(true).unwrap();
        let mut info = next(&mut incoming, "info").await;
        info.write_all(&header(0)).await.unwrap();
        let mut resumed = next(&mut incoming, "opus").await;
        resumed.write_all(&header(0)).await.unwrap();
        resumed.write_all(&header(3)).await.unwrap();
        resumed.write_all(&[0xfc, 0xff, 0xfe]).await.unwrap();
        time::timeout(Duration::from_secs(1), async {
            loop {
                state.changed().await.unwrap();
                let current = state.borrow_and_update().clone();
                if current.generation > first_generation
                    && current
                        .packet
                        .as_ref()
                        .is_some_and(|(_, data)| data[..] == [0xfc, 0xff, 0xfe])
                {
                    break;
                }
            }
        })
        .await
        .unwrap();
        task.abort();
        let _ = task.await;
        assert!(resumed.read_u8().await.is_err());
        accept.abort();
        let _ = accept.await;
        std::fs::remove_dir_all(dir).unwrap();
    }
}
