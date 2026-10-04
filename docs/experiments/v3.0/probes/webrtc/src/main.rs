//! Qualification only: two isolated loopback peers, no external STUN/TURN.
use std::{sync::Arc, time::Duration};
use tokio::sync::mpsc;
use webrtc::{
    data_channel::{DataChannel, DataChannelEvent},
    peer_connection::{
        register_default_interceptors, MediaEngine, PeerConnection, PeerConnectionBuilder,
        PeerConnectionEventHandler, RTCConfigurationBuilder, RTCIceGatheringState, RTCIceServer,
        RTCIceTransportPolicy, Registry,
    },
};
type Error = Box<dyn std::error::Error + Send + Sync>;

struct Handler {
    gathered: mpsc::Sender<()>,
    received: mpsc::Sender<String>,
}
#[async_trait::async_trait]
impl PeerConnectionEventHandler for Handler {
    async fn on_ice_gathering_state_change(&self, state: RTCIceGatheringState) {
        if state == RTCIceGatheringState::Complete {
            let _ = self.gathered.try_send(());
        }
    }
    async fn on_data_channel(&self, dc: Arc<dyn DataChannel>) {
        let received = self.received.clone();
        // The callback must return so the peer driver can deliver events.
        tokio::spawn(async move {
            while let Some(event) = dc.poll().await {
                match event {
                    DataChannelEvent::OnMessage(msg) => {
                        let text = String::from_utf8_lossy(&msg.data).into_owned();
                        let _ = received.try_send(text);
                        if let Err(e) = dc.send_text("nk-v3-ack").await {
                            let _ = received.try_send(format!("echo error: {e}"));
                        }
                    }
                    DataChannelEvent::OnClose => break,
                    _ => {}
                }
            }
        });
    }
}
async fn peer(
    turn: Option<&str>,
) -> Result<
    (
        Arc<dyn PeerConnection>,
        mpsc::Receiver<()>,
        mpsc::Receiver<String>,
    ),
    Error,
> {
    let (gathered, gather_rx) = mpsc::channel(1);
    let (received, receive_rx) = mpsc::channel(4);
    let mut media = MediaEngine::default();
    media.register_default_codecs()?;
    let registry = register_default_interceptors(Registry::new(), &mut media)?;
    let mut configuration = RTCConfigurationBuilder::new();
    if let Some(address) = turn {
        let address: std::net::SocketAddr = address.parse()?;
        if !address.ip().is_loopback() {
            return Err("qualification TURN must be loopback".into());
        }
        configuration = configuration
            .with_ice_transport_policy(RTCIceTransportPolicy::Relay)
            .with_ice_servers(vec![RTCIceServer {
                urls: vec![format!("turn:{address}?transport=udp")],
                username: "v3-test".into(),
                credential: "test-only-password".into(),
            }]);
    }
    let pc = PeerConnectionBuilder::new()
        .with_configuration(configuration.build())
        .with_media_engine(media)
        .with_interceptor_registry(registry)
        .with_handler(Arc::new(Handler { gathered, received }))
        .with_runtime(webrtc::runtime::default_runtime().ok_or("Tokio runtime unavailable")?)
        .with_udp_addrs(vec!["127.0.0.1:0".to_owned()])
        .with_data_channel_send_buffer_limit(64 * 1024)
        .build()
        .await?;
    Ok((Arc::new(pc), gather_rx, receive_rx))
}

async fn exchange(turn: Option<&str>) -> Result<(), Error> {
    let (a, mut ga, _) = peer(turn).await?;
    let (b, mut gb, mut received) = peer(turn).await?;
    let dc = a.create_data_channel("hid-qualification", None).await?;
    a.set_local_description(a.create_offer(None).await?).await?;
    ga.recv().await.ok_or("offer gathering stopped")?;
    let offer = a.local_description().await.ok_or("missing offer")?;
    if turn.is_some() && (!offer.sdp.contains(" typ relay") || offer.sdp.contains(" typ host")) {
        return Err("TURN-only offer did not gather exclusively relay candidates".into());
    }
    b.set_remote_description(offer).await?;
    b.set_local_description(b.create_answer(None).await?)
        .await?;
    gb.recv().await.ok_or("answer gathering stopped")?;
    a.set_remote_description(b.local_description().await.ok_or("missing answer")?)
        .await?;
    let mut opened = false;
    loop {
        match dc.poll().await.ok_or("data channel closed")? {
            DataChannelEvent::OnOpen => {
                opened = true;
                dc.send_text("nk-v3-probe").await?;
            }
            DataChannelEvent::OnMessage(msg) => {
                if !opened || msg.data.as_ref() != b"nk-v3-ack" {
                    return Err("unexpected data channel response".into());
                }
                break;
            }
            DataChannelEvent::OnClose => return Err("data channel closed early".into()),
            _ => {}
        }
    }
    if received.recv().await.as_deref() != Some("nk-v3-probe") {
        return Err("remote data channel payload mismatch".into());
    }
    a.close().await?;
    b.close().await?;
    println!(
        "webrtc-rs 0.21.0: ICE + DTLS + SCTP bidirectional loopback PASS; TURN relay-only={}",
        turn.is_some()
    );
    Ok(())
}

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<(), Error> {
    let mut args = std::env::args().skip(1);
    let turn = match args.next().as_deref() {
        None => None,
        Some("--turn") => Some(args.next().ok_or("--turn requires an address")?),
        _ => return Err("unexpected probe argument".into()),
    };
    tokio::time::timeout(Duration::from_secs(30), exchange(turn.as_deref())).await?
}
