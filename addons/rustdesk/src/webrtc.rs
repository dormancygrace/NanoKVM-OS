// RustDesk 1.5 WebRTC adapter. SPDX-License-Identifier: AGPL-3.0-only
use crate::{
    config::Config,
    identity::RustDeskIdentity,
    onekvm::Identity,
    protocol::{rendezvous_message, IceCandidate, PunchHole, PunchHoleSent, RendezvousMessage},
    server::{self, ClientLimits},
    signaling,
};
use base64::{engine::general_purpose::STANDARD, Engine};
use serde::{Deserialize, Serialize};
use std::{
    collections::{HashMap, HashSet},
    io,
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt},
    net::UnixStream,
    sync::{mpsc, watch},
    time,
};
const SOCKET: &str = "/run/nanokvm-rustdesk/webrtc.sock";
const MAX_SIGNAL: usize = 64 * 1024;

#[derive(Serialize, Default)]
struct Command<'a> {
    op: &'a str,
    #[serde(skip_serializing_if = "Option::is_none")]
    offer: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    token: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    candidate: Option<&'a str>,
}
#[derive(Deserialize, Debug)]
struct Event {
    event: String,
    #[serde(default)]
    answer: String,
    #[serde(default)]
    token: String,
    #[serde(default)]
    fingerprint: String,
    #[serde(default)]
    session_key: String,
    #[serde(default)]
    candidate: String,
    #[serde(default)]
    error: String,
}
async fn read_rpc<R: AsyncRead + Unpin>(r: &mut R) -> io::Result<Event> {
    let n = r.read_u32_le().await? as usize;
    if n == 0 || n > MAX_SIGNAL {
        return Err(invalid("oversized WebRTC RPC"));
    }
    let mut data = vec![0; n];
    r.read_exact(&mut data).await?;
    serde_json::from_slice(&data).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}
async fn write_rpc<W: AsyncWrite + Unpin>(w: &mut W, c: Command<'_>) -> io::Result<()> {
    let data = serde_json::to_vec(&c).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    if data.len() > MAX_SIGNAL {
        return Err(invalid("oversized WebRTC RPC"));
    }
    time::timeout(Duration::from_secs(3), async {
        w.write_u32_le(data.len() as u32).await?;
        w.write_all(&data).await
    })
    .await
    .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "WebRTC RPC write timed out"))?
}
fn invalid(s: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, s)
}
fn offer_key(endpoint: &str, force_relay: bool) -> io::Result<String> {
    if endpoint.len() > 48 * 1024 {
        return Err(invalid("oversized SDP offer"));
    }
    let data = STANDARD
        .decode(
            endpoint
                .strip_prefix("webrtc://")
                .ok_or_else(|| invalid("invalid SDP endpoint"))?,
        )
        .map_err(|_| invalid("invalid SDP encoding"))?;
    let value: serde_json::Value =
        serde_json::from_slice(&data).map_err(|_| invalid("invalid SDP offer"))?;
    if value["type"] != "offer" || (force_relay && value["ice_policy"] != "all") {
        return Err(invalid("unsupported WebRTC relay policy"));
    }
    fingerprint(
        value["sdp"]
            .as_str()
            .ok_or_else(|| invalid("missing SDP"))?,
    )
}
fn fingerprint(sdp: &str) -> io::Result<String> {
    let mut found = None;
    for line in sdp.lines() {
        if let Some(raw) = line.strip_prefix("a=fingerprint:sha-256 ") {
            let raw = raw.trim().to_ascii_uppercase();
            if raw.len() != 95
                || raw.split(':').count() != 32
                || raw
                    .split(':')
                    .any(|p| p.len() != 2 || !p.bytes().all(|c| c.is_ascii_hexdigit()))
            {
                return Err(invalid("invalid DTLS fingerprint"));
            }
            let fp = format!("sha-256 {raw}");
            if found.as_ref().is_some_and(|previous| previous != &fp) {
                return Err(invalid("conflicting DTLS fingerprints"));
            }
            found = Some(fp);
        }
    }
    found.ok_or_else(|| invalid("missing DTLS fingerprint"))
}
struct Entry {
    offer: String,
    token: u64,
    answer: Option<String>,
    candidates: mpsc::Sender<String>,
    seen: HashSet<[u8; 32]>,
}
#[derive(Clone, Default)]
pub struct Manager {
    entries: Arc<Mutex<HashMap<String, Entry>>>,
}
struct Slot {
    owner: Manager,
    key: String,
    token: u64,
}
impl Drop for Slot {
    fn drop(&mut self) {
        let mut entries = self.owner.entries.lock().unwrap_or_else(|e| e.into_inner());
        if entries
            .get(&self.key)
            .is_some_and(|e| e.token == self.token)
        {
            entries.remove(&self.key);
        }
    }
}
struct AbortTask<T>(tokio::task::JoinHandle<T>);
impl<T> Drop for AbortTask<T> {
    fn drop(&mut self) {
        self.0.abort();
    }
}

impl Manager {
    pub fn candidate(&self, c: IceCandidate) {
        if c.candidate.len() > 8192 {
            return;
        }
        let mut entries = self.entries.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(e) = entries.get_mut(&c.session_key) {
            use sha2::{Digest, Sha256};
            let digest: [u8; 32] = Sha256::digest(c.candidate.as_bytes()).into();
            if e.seen.len() >= 128 || e.seen.contains(&digest) {
                return;
            }
            if e.candidates.try_send(c.candidate).is_ok() {
                e.seen.insert(digest);
            }
        }
    }
    #[allow(clippy::too_many_arguments)]
    pub async fn answer(
        &self,
        request: &PunchHole,
        config: Arc<Config>,
        identity: Arc<Identity>,
        rd: Arc<RustDeskIdentity>,
        mut shutdown: watch::Receiver<bool>,
        limits: ClientLimits,
    ) -> io::Result<()> {
        let key = offer_key(&request.webrtc_sdp_offer, request.force_relay)?;
        let (tx, mut rx) = mpsc::channel::<String>(32);
        let token = rand::random::<u64>();
        let mut slot = None;
        let existing = {
            let mut entries = self.entries.lock().unwrap_or_else(|e| e.into_inner());
            if let Some(e) = entries.get(&key) {
                if e.offer != request.webrtc_sdp_offer {
                    return Err(invalid("DTLS identity reused with different SDP"));
                }
                Some(e.answer.clone())
            } else {
                if entries.len() >= 4 {
                    return Err(io::Error::new(
                        io::ErrorKind::WouldBlock,
                        "WebRTC session limit",
                    ));
                }
                entries.insert(
                    key.clone(),
                    Entry {
                        offer: request.webrtc_sdp_offer.clone(),
                        token,
                        answer: None,
                        candidates: tx,
                        seen: HashSet::new(),
                    },
                );
                slot = Some(Slot {
                    owner: self.clone(),
                    key: key.clone(),
                    token,
                });
                None
            }
        };
        if let Some(existing) = existing {
            // hbbs retries offers while setup is pending. One answerer owns each SDP.
            if let Some(answer) = existing {
                let _pending = limits.try_pending().ok_or_else(|| {
                    io::Error::new(io::ErrorKind::WouldBlock, "pending handshake limit")
                })?;
                let mut signal = signaling::connect(&config).await?;
                signal.writer.write(&response(request, &rd, answer)).await?;
            }
            return Ok(());
        }
        let _slot = slot;
        let pending = limits
            .try_pending()
            .ok_or_else(|| io::Error::new(io::ErrorKind::WouldBlock, "pending handshake limit"))?;
        let mut signal = signaling::connect(&config).await?;
        let mut rpc = UnixStream::connect(SOCKET).await?;
        write_rpc(
            &mut rpc,
            Command {
                op: "offer",
                offer: Some(&request.webrtc_sdp_offer),
                ..Default::default()
            },
        )
        .await?;
        let reply = time::timeout(Duration::from_secs(8), read_rpc(&mut rpc))
            .await
            .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "WebRTC answer timed out"))??;
        if reply.event != "answer" {
            return Err(invalid(&format!("WebRTC bridge: {}", reply.error)));
        }
        if reply.session_key != key
            || reply.token.len() != 64
            || !reply.token.bytes().all(|c| c.is_ascii_hexdigit())
        {
            return Err(invalid("WebRTC bridge identity mismatch"));
        }
        let answer_data = STANDARD
            .decode(
                reply
                    .answer
                    .strip_prefix("webrtc://")
                    .ok_or_else(|| invalid("invalid SDP answer"))?,
            )
            .map_err(|_| invalid("invalid SDP answer"))?;
        let answer_json: serde_json::Value =
            serde_json::from_slice(&answer_data).map_err(|_| invalid("invalid SDP answer"))?;
        if answer_json["type"] != "answer"
            || fingerprint(answer_json["sdp"].as_str().unwrap_or(""))? != reply.fingerprint
        {
            return Err(invalid("WebRTC answer fingerprint mismatch"));
        }
        signal
            .writer
            .write(&response(request, &rd, reply.answer.clone()))
            .await?;
        if let Some(e) = self
            .entries
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get_mut(&key)
        {
            e.answer = Some(reply.answer);
        }
        let (mut rpc_reader, mut rpc_writer) = tokio::io::split(rpc);
        let (events_tx, mut events) = mpsc::channel(32);
        let _reader = AbortTask(tokio::spawn(async move {
            loop {
                match read_rpc(&mut rpc_reader).await {
                    Ok(e) => {
                        if events_tx.send(e).await.is_err() {
                            return;
                        }
                    }
                    Err(_) => return,
                }
            }
        }));
        let peer = crate::rendezvous::decode_address(&request.socket_addr);
        drop(signal);
        let signal_config = Arc::clone(&config);
        let mut candidate_signal: Option<signaling::Signal> = None;
        let mut connected = false;
        let data_shutdown = shutdown.clone();
        let data = async {
            let mut stream = UnixStream::connect(SOCKET).await?;
            write_rpc(
                &mut stream,
                Command {
                    op: "attach",
                    token: Some(&reply.token),
                    ..Default::default()
                },
            )
            .await?;
            let ready = time::timeout(Duration::from_secs(20), read_rpc(&mut stream))
                .await
                .map_err(|_| {
                    io::Error::new(io::ErrorKind::TimedOut, "WebRTC data channel timed out")
                })??;
            if ready.event != "ready" {
                return Err(invalid("WebRTC data channel unavailable"));
            }
            server::serve_webrtc(
                stream,
                reply.fingerprint,
                peer,
                config,
                identity,
                rd,
                data_shutdown,
                limits,
                pending,
            )
            .await
        };
        tokio::pin!(data);
        let outcome = async {
        loop {
            tokio::select! {
                event=events.recv()=>match event {
                    Some(e) if e.event=="candidate" && e.candidate.len()<=8192 => {
                        let candidate = RendezvousMessage {union:Some(rendezvous_message::Union::IceCandidate(IceCandidate {
                            id:String::new(),socket_addr:request.socket_addr.clone(),session_key:key.clone(),candidate:e.candidate,
                        }))};
                        // hbbs closes answer connections; trickled ICE has its own persistent encrypted connection.
                        for _ in 0..2 {
                            if candidate_signal.is_none() {
                                match signaling::connect(&signal_config).await {
                                    Ok(s)=>candidate_signal=Some(s),
                                    Err(_)=>break,
                                }
                            }
                            let sent=time::timeout(Duration::from_secs(3),candidate_signal.as_mut().unwrap().writer.write(&candidate)).await;
                            if matches!(sent,Ok(Ok(()))) {break;}
                            candidate_signal=None;
                        }
                    }
                    Some(e) if e.event=="connected" => {connected=true;},
                    Some(_) => return Err(invalid("unexpected WebRTC event")),
                    None => return Err(io::Error::new(io::ErrorKind::BrokenPipe,"WebRTC bridge closed")),
                },
                c=rx.recv()=>match c {
                    Some(c)=>write_rpc(&mut rpc_writer,Command {op:"candidate",candidate:Some(&c),..Default::default()}).await?,
                    None=>return Ok(()),
                },
                result=&mut data=>return result,
                _=shutdown.changed()=>return Ok(()),
            }
        }
        }.await;
        if connected {
            if let Err(e) = outcome {
                eprintln!("RustDesk WebRTC session: {e}");
            }
            Ok(())
        } else {
            outcome
        }
    }
}
fn response(request: &PunchHole, rd: &RustDeskIdentity, answer: String) -> RendezvousMessage {
    RendezvousMessage {
        union: Some(rendezvous_message::Union::PunchHoleSent(PunchHoleSent {
            socket_addr: request.socket_addr.clone(),
            socket_addr_v6: request.socket_addr_v6.clone(),
            id: rd.id.clone(),
            relay_server: request.relay_server.clone(),
            nat_type: crate::protocol::NatType::Unknown as i32,
            version: crate::upstream::version().to_owned(),
            webrtc_sdp_answer: answer,
            ..Default::default()
        })),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn policy_and_fingerprint_are_strict() {
        let fp = (0..32).map(|_| "AB").collect::<Vec<_>>().join(":");
        let sdp = format!("v=0\r\na=fingerprint:sha-256 {fp}\r\n");
        let endpoint = |policy: &str| {
            format!(
                "webrtc://{}",
                STANDARD.encode(
                    serde_json::json!({"type":"offer","sdp":sdp,"ice_policy":policy}).to_string()
                )
            )
        };
        assert!(offer_key(&endpoint("all"), true).is_ok());
        assert!(offer_key(&endpoint("relay"), true).is_err());
        assert!(offer_key(&endpoint("relay"), false).is_ok());
        assert!(fingerprint(&format!(
            "{sdp}a=fingerprint:sha-256 {}\n",
            fp.replace("AB", "CD")
        ))
        .is_err());
        assert!(offer_key("webrtc://?", false).is_err());
    }
    #[tokio::test]
    async fn rpc_read_limits_and_partial_reads() {
        let (mut w, mut r) = tokio::io::duplex(128);
        let writer = tokio::spawn(async move {
            for b in [7, 0, 0, 0, b'{', b'"', b'e', b'v', b'e', b'n', b't'] {
                w.write_u8(b).await.unwrap();
            }
        });
        assert!(read_rpc(&mut r).await.is_err());
        writer.await.unwrap();
        let (mut w, mut r) = tokio::io::duplex(8);
        w.write_u32_le((MAX_SIGNAL + 1) as u32).await.unwrap();
        assert!(read_rpc(&mut r).await.is_err());
    }
}
