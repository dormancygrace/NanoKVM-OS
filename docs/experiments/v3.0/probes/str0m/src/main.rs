//! Qualification only: drive two SansIO peers through real loopback UDP.
use std::{
    io::ErrorKind,
    net::UdpSocket,
    time::{Duration, Instant},
};
use str0m::{
    Candidate, Event, Input, Output, Rtc,
    net::{Protocol, Receive},
};
type Error = Box<dyn std::error::Error + Send + Sync>;

struct Peer {
    rtc: Rtc,
    socket: UdpSocket,
    initiator: bool,
    received: bool,
}
impl Peer {
    fn new(initiator: bool, now: Instant) -> Result<Self, Error> {
        let socket = UdpSocket::bind("127.0.0.1:0")?;
        socket.set_nonblocking(true)?;
        let mut rtc = Rtc::new(now);
        rtc.add_local_candidate(Candidate::host(socket.local_addr()?, "udp")?)
            .ok_or("local ICE candidate rejected")?;
        let mut peer = Self {
            rtc,
            socket,
            initiator,
            received: false,
        };
        peer.drain()?;
        Ok(peer)
    }
    fn drain(&mut self) -> Result<(), Error> {
        loop {
            match self.rtc.poll_output()? {
                Output::Transmit(tx) => {
                    self.socket.send_to(&tx.contents, tx.destination)?;
                }
                Output::Timeout(_) => return Ok(()),
                Output::Event(Event::ChannelOpen(id, _)) if self.initiator => {
                    if !self
                        .rtc
                        .channel(id)
                        .ok_or("channel missing")?
                        .write(false, b"nk-v3-probe")?
                    {
                        return Err("send buffer full".into());
                    }
                }
                Output::Event(Event::ChannelData(data)) => {
                    let expected = if self.initiator {
                        b"nk-v3-ack".as_slice()
                    } else {
                        b"nk-v3-probe".as_slice()
                    };
                    if data.data != expected || data.binary {
                        return Err("payload mismatch".into());
                    }
                    self.received = true;
                    if !self.initiator
                        && !self
                            .rtc
                            .channel(data.id)
                            .ok_or("echo channel missing")?
                            .write(false, b"nk-v3-ack")?
                    {
                        return Err("echo send buffer full".into());
                    }
                }
                _ => {}
            }
        }
    }
    fn tick(&mut self) -> Result<(), Error> {
        self.rtc.handle_input(Input::Timeout(Instant::now()))?;
        self.drain()?;
        let mut buffer = [0u8; 65536];
        loop {
            match self.socket.recv_from(&mut buffer) {
                Ok((n, source)) => {
                    let receive = Receive::new(
                        Protocol::Udp,
                        source,
                        self.socket.local_addr()?,
                        &buffer[..n],
                    )?;
                    self.rtc
                        .handle_input(Input::Receive(Instant::now(), receive))?;
                    self.drain()?;
                }
                Err(e) if e.kind() == ErrorKind::WouldBlock => return Ok(()),
                Err(e) => return Err(e.into()),
            }
        }
    }
}

fn main() -> Result<(), Error> {
    str0m::crypto::from_feature_flags().install_process_default();
    let start = Instant::now();
    let mut a = Peer::new(true, start)?;
    let mut b = Peer::new(false, start)?;
    let mut changes = a.rtc.sdp_api();
    changes.add_channel("hid-qualification".into());
    let (offer, pending) = changes.apply().ok_or("SDP offer missing")?;
    a.drain()?;
    let answer = b.rtc.sdp_api().accept_offer(offer)?;
    b.drain()?;
    a.rtc.sdp_api().accept_answer(pending, answer)?;
    a.drain()?;
    while !(a.received && b.received) {
        if start.elapsed() > Duration::from_secs(30) {
            return Err("peer exchange timed out".into());
        }
        a.tick()?;
        b.tick()?;
        std::thread::sleep(Duration::from_millis(1));
    }
    a.rtc.disconnect();
    b.rtc.disconnect();
    println!("str0m 0.24.1: ICE + DTLS + SCTP bidirectional loopback PASS");
    Ok(())
}
