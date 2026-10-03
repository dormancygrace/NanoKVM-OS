// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
#![allow(dead_code)]

use prost::{Enumeration, Message as ProstMessage, Oneof};

#[derive(Clone, PartialEq, ProstMessage)]
pub struct IdPk {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(bytes = "vec", tag = "2")]
    pub pk: Vec<u8>,
    #[prost(string, tag = "3")]
    pub dtls_fingerprint: String,
    #[prost(uint32, tag = "4")]
    pub kx_version: u32,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct PublicKey {
    #[prost(bytes = "vec", tag = "1")]
    pub asymmetric_value: Vec<u8>,
    #[prost(bytes = "vec", tag = "2")]
    pub symmetric_value: Vec<u8>,
    // RustDesk 1.5.0: absent means the original KX v0 scheme.
    #[prost(uint32, tag = "3")]
    pub kx_version: u32,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct SignedId {
    #[prost(bytes = "vec", tag = "1")]
    pub id: Vec<u8>,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct EncodedVideoFrame {
    #[prost(bytes = "bytes", tag = "1")]
    pub data: bytes::Bytes,
    #[prost(bool, tag = "2")]
    pub key: bool,
    #[prost(int64, tag = "3")]
    pub pts: i64,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct EncodedVideoFrames {
    #[prost(message, repeated, tag = "1")]
    pub frames: Vec<EncodedVideoFrame>,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct VideoFrame {
    #[prost(oneof = "video_frame::Union", tags = "10, 11")]
    pub union: Option<video_frame::Union>,
    #[prost(int32, tag = "14")]
    pub display: i32,
}

pub mod video_frame {
    use super::{EncodedVideoFrames, Oneof};

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(message, tag = "10")]
        H264s(EncodedVideoFrames),
        #[prost(message, tag = "11")]
        H265s(EncodedVideoFrames),
    }
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct DisplayInfo {
    #[prost(sint32, tag = "1")]
    pub x: i32,
    #[prost(sint32, tag = "2")]
    pub y: i32,
    #[prost(int32, tag = "3")]
    pub width: i32,
    #[prost(int32, tag = "4")]
    pub height: i32,
    #[prost(string, tag = "5")]
    pub name: String,
    #[prost(bool, tag = "6")]
    pub online: bool,
    #[prost(bool, tag = "7")]
    pub cursor_embedded: bool,
    #[prost(double, tag = "9")]
    pub scale: f64,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct CodecAbility {
    #[prost(bool, tag = "1")]
    pub vp8: bool,
    #[prost(bool, tag = "2")]
    pub vp9: bool,
    #[prost(bool, tag = "3")]
    pub av1: bool,
    #[prost(bool, tag = "4")]
    pub h264: bool,
    #[prost(bool, tag = "5")]
    pub h265: bool,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct SupportedEncoding {
    #[prost(bool, tag = "1")]
    pub h264: bool,
    #[prost(bool, tag = "2")]
    pub h265: bool,
    #[prost(bool, tag = "3")]
    pub vp8: bool,
    #[prost(bool, tag = "4")]
    pub av1: bool,
    #[prost(message, optional, tag = "5")]
    pub i444: Option<CodecAbility>,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct Features {
    #[prost(bool, tag = "1")]
    pub privacy_mode: bool,
    #[prost(bool, tag = "2")]
    pub terminal: bool,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct PeerInfo {
    #[prost(string, tag = "1")]
    pub username: String,
    #[prost(string, tag = "2")]
    pub hostname: String,
    #[prost(string, tag = "3")]
    pub platform: String,
    #[prost(message, repeated, tag = "4")]
    pub displays: Vec<DisplayInfo>,
    #[prost(int32, tag = "5")]
    pub current_display: i32,
    #[prost(bool, tag = "6")]
    pub sas_enabled: bool,
    #[prost(string, tag = "7")]
    pub version: String,
    #[prost(message, optional, tag = "9")]
    pub features: Option<Features>,
    #[prost(message, optional, tag = "10")]
    pub encoding: Option<SupportedEncoding>,
    #[prost(string, tag = "12")]
    pub platform_additions: String,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct LoginResponse {
    #[prost(oneof = "login_response::Union", tags = "1, 2")]
    pub union: Option<login_response::Union>,
    #[prost(bool, tag = "3")]
    pub enable_trusted_devices: bool,
}

pub mod login_response {
    use super::{Oneof, PeerInfo};

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(string, tag = "1")]
        Error(String),
        #[prost(message, tag = "2")]
        PeerInfo(PeerInfo),
    }
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct Empty {}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct LoginRequest {
    #[prost(string, tag = "1")]
    pub username: String,
    #[prost(bytes = "vec", tag = "2")]
    pub password: Vec<u8>,
    #[prost(string, tag = "4")]
    pub my_id: String,
    #[prost(string, tag = "5")]
    pub my_name: String,
    #[prost(message, optional, tag = "6")]
    pub option: Option<OptionMessage>,
    #[prost(oneof = "login_request::Union", tags = "7, 8, 15, 16")]
    pub union: Option<login_request::Union>,
    #[prost(bool, tag = "9")]
    pub video_ack_required: bool,
    #[prost(uint64, tag = "10")]
    pub session_id: u64,
    #[prost(string, tag = "11")]
    pub version: String,
    #[prost(string, tag = "13")]
    pub my_platform: String,
}

pub mod login_request {
    use super::{Empty, Oneof};

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(message, tag = "7")]
        FileTransfer(Empty),
        #[prost(message, tag = "8")]
        PortForward(Empty),
        #[prost(message, tag = "15")]
        ViewCamera(Empty),
        #[prost(message, tag = "16")]
        Terminal(Empty),
    }
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct SupportedDecoding {
    #[prost(int32, tag = "1")]
    pub ability_vp9: i32,
    #[prost(int32, tag = "2")]
    pub ability_h264: i32,
    #[prost(int32, tag = "3")]
    pub ability_h265: i32,
    #[prost(int32, tag = "4")]
    pub prefer: i32,
    #[prost(int32, tag = "5")]
    pub ability_vp8: i32,
    #[prost(int32, tag = "6")]
    pub ability_av1: i32,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct OptionMessage {
    #[prost(message, optional, tag = "10")]
    pub supported_decoding: Option<SupportedDecoding>,
    #[prost(int32, tag = "11")]
    pub custom_fps: i32,
    #[prost(int32, tag = "12")]
    pub disable_keyboard: i32,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct Hash {
    #[prost(string, tag = "1")]
    pub salt: String,
    #[prost(string, tag = "2")]
    pub challenge: String,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct TestDelay {
    #[prost(int64, tag = "1")]
    pub time: i64,
    #[prost(bool, tag = "2")]
    pub from_client: bool,
    #[prost(uint32, tag = "3")]
    pub last_delay: u32,
    #[prost(uint32, tag = "4")]
    pub target_bitrate: u32,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct MouseEvent {
    #[prost(int32, tag = "1")]
    pub mask: i32,
    #[prost(sint32, tag = "2")]
    pub x: i32,
    #[prost(sint32, tag = "3")]
    pub y: i32,
    #[prost(enumeration = "ControlKey", repeated, tag = "4")]
    pub modifiers: Vec<i32>,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct KeyEvent {
    #[prost(bool, tag = "1")]
    pub down: bool,
    #[prost(bool, tag = "2")]
    pub press: bool,
    #[prost(oneof = "key_event::Union", tags = "3, 4, 5, 6, 7")]
    pub union: Option<key_event::Union>,
    #[prost(enumeration = "ControlKey", repeated, tag = "8")]
    pub modifiers: Vec<i32>,
    #[prost(enumeration = "KeyboardMode", tag = "9")]
    pub mode: i32,
}

pub mod key_event {
    use super::Oneof;

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(enumeration = "super::ControlKey", tag = "3")]
        ControlKey(i32),
        #[prost(uint32, tag = "4")]
        Chr(u32),
        #[prost(uint32, tag = "5")]
        Unicode(u32),
        #[prost(string, tag = "6")]
        Seq(String),
        #[prost(uint32, tag = "7")]
        Win2winHotkey(u32),
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Enumeration)]
#[repr(i32)]
pub enum KeyboardMode {
    Legacy = 0,
    Map = 1,
    Translate = 2,
    Auto = 3,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Enumeration)]
#[repr(i32)]
pub enum ControlKey {
    Unknown = 0,
    Alt = 1,
    Backspace = 2,
    CapsLock = 3,
    Control = 4,
    Delete = 5,
    DownArrow = 6,
    End = 7,
    Escape = 8,
    F1 = 9,
    F10 = 10,
    F11 = 11,
    F12 = 12,
    F2 = 13,
    F3 = 14,
    F4 = 15,
    F5 = 16,
    F6 = 17,
    F7 = 18,
    F8 = 19,
    F9 = 20,
    Home = 21,
    LeftArrow = 22,
    Meta = 23,
    Option = 24,
    PageDown = 25,
    PageUp = 26,
    Return = 27,
    RightArrow = 28,
    Shift = 29,
    Space = 30,
    Tab = 31,
    UpArrow = 32,
    Numpad0 = 33,
    Numpad1 = 34,
    Numpad2 = 35,
    Numpad3 = 36,
    Numpad4 = 37,
    Numpad5 = 38,
    Numpad6 = 39,
    Numpad7 = 40,
    Numpad8 = 41,
    Numpad9 = 42,
    Pause = 46,
    Snapshot = 57,
    Insert = 58,
    Scroll = 62,
    NumLock = 63,
    RWin = 64,
    Apps = 65,
    Multiply = 66,
    Add = 67,
    Subtract = 68,
    Decimal = 69,
    Divide = 70,
    Equals = 71,
    NumpadEnter = 72,
    RShift = 73,
    RControl = 74,
    RAlt = 75,
    CtrlAltDel = 100,
    LockScreen = 101,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct Misc {
    #[prost(oneof = "misc::Union", tags = "7,9")]
    pub union: Option<misc::Union>,
}
pub mod misc {
    use super::{Oneof, OptionMessage};
    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(message, tag = "7")]
        Option(OptionMessage),
        #[prost(string, tag = "9")]
        CloseReason(String),
    }
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct Message {
    #[prost(oneof = "message::Union", tags = "3, 4, 5, 6, 7, 8, 9, 10, 15,19")]
    pub union: Option<message::Union>,
}

pub mod message {
    use super::{
        Hash, KeyEvent, LoginRequest, LoginResponse, Misc, MouseEvent, Oneof, PublicKey, SignedId,
        TestDelay, VideoFrame,
    };

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(message, tag = "3")]
        SignedId(SignedId),
        #[prost(message, tag = "4")]
        PublicKey(PublicKey),
        #[prost(message, tag = "5")]
        TestDelay(TestDelay),
        #[prost(message, tag = "6")]
        VideoFrame(VideoFrame),
        #[prost(message, tag = "7")]
        LoginRequest(LoginRequest),
        #[prost(message, tag = "8")]
        LoginResponse(LoginResponse),
        #[prost(message, tag = "9")]
        Hash(Hash),
        #[prost(message, tag = "10")]
        MouseEvent(MouseEvent),
        #[prost(message, tag = "15")]
        KeyEvent(KeyEvent),
        #[prost(message, tag = "19")]
        Misc(Misc),
    }
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct RegisterPeer {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(int32, tag = "2")]
    pub serial: i32,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct RegisterPeerResponse {
    #[prost(bool, tag = "2")]
    pub request_pk: bool,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct PunchHoleRequest {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(enumeration = "NatType", tag = "2")]
    pub nat_type: i32,
    #[prost(string, tag = "3")]
    pub licence_key: String,
    #[prost(enumeration = "ConnType", tag = "4")]
    pub conn_type: i32,
    #[prost(string, tag = "5")]
    pub token: String,
    #[prost(string, tag = "6")]
    pub version: String,
    #[prost(int32, tag = "7")]
    pub udp_port: i32,
    #[prost(bool, tag = "8")]
    pub force_relay: bool,
    #[prost(int32, tag = "9")]
    pub upnp_port: i32,
    #[prost(bytes = "vec", tag = "10")]
    pub socket_addr_v6: Vec<u8>,
    #[prost(string, tag = "11")]
    pub switch_code: String,
    #[prost(string, tag = "12")]
    pub webrtc_sdp_offer: String,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct RegisterPk {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(bytes = "vec", tag = "2")]
    pub uuid: Vec<u8>,
    #[prost(bytes = "vec", tag = "3")]
    pub pk: Vec<u8>,
    #[prost(string, tag = "4")]
    pub old_id: String,
    #[prost(bool, tag = "5")]
    pub no_register_device: bool,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct RegisterPkResponse {
    #[prost(enumeration = "RegisterPkResult", tag = "1")]
    pub result: i32,
    #[prost(int32, tag = "2")]
    pub keep_alive: i32,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Enumeration)]
#[repr(i32)]
pub enum RegisterPkResult {
    Ok = 0,
    UuidMismatch = 2,
    IdExists = 3,
    TooFrequent = 4,
    InvalidIdFormat = 5,
    NotSupport = 6,
    ServerError = 7,
    NotDeployed = 8,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct ControlPermissions {
    #[prost(uint64, tag = "1")]
    pub permissions: u64,
}
#[derive(Clone, PartialEq, ProstMessage)]
pub struct ControlledContext {
    #[prost(string, tag = "1")]
    pub conn_audit_ref: String,
}
#[derive(Clone, PartialEq, ProstMessage)]
pub struct PunchHoleSent {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "2")]
    pub id: String,
    #[prost(string, tag = "3")]
    pub relay_server: String,
    #[prost(enumeration = "NatType", tag = "4")]
    pub nat_type: i32,
    #[prost(string, tag = "5")]
    pub version: String,
    #[prost(int32, tag = "6")]
    pub upnp_port: i32,
    #[prost(bytes = "vec", tag = "7")]
    pub socket_addr_v6: Vec<u8>,
    #[prost(string, tag = "8")]
    pub webrtc_sdp_answer: String,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct PunchHole {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "2")]
    pub relay_server: String,
    #[prost(enumeration = "NatType", tag = "3")]
    pub nat_type: i32,
    #[prost(int32, tag = "4")]
    pub udp_port: i32,
    #[prost(bool, tag = "5")]
    pub force_relay: bool,
    #[prost(int32, tag = "6")]
    pub upnp_port: i32,
    #[prost(bytes = "vec", tag = "7")]
    pub socket_addr_v6: Vec<u8>,
    #[prost(string, tag = "10")]
    pub webrtc_sdp_offer: String,
    #[prost(message, optional, tag = "8")]
    pub control_permissions: Option<ControlPermissions>,
    #[prost(message, optional, tag = "9")]
    pub controlled_context: Option<ControlledContext>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Enumeration)]
#[repr(i32)]
pub enum NatType {
    Unknown = 0,
    Asymmetric = 1,
    Symmetric = 2,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct FetchLocalAddr {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "2")]
    pub relay_server: String,
    #[prost(bytes = "vec", tag = "3")]
    pub socket_addr_v6: Vec<u8>,
    #[prost(message, optional, tag = "4")]
    pub control_permissions: Option<ControlPermissions>,
    #[prost(message, optional, tag = "5")]
    pub controlled_context: Option<ControlledContext>,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct LocalAddr {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(bytes = "vec", tag = "2")]
    pub local_addr: Vec<u8>,
    #[prost(string, tag = "3")]
    pub relay_server: String,
    #[prost(string, tag = "4")]
    pub id: String,
    #[prost(string, tag = "5")]
    pub version: String,
    #[prost(bytes = "vec", tag = "6")]
    pub socket_addr_v6: Vec<u8>,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct RequestRelay {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(string, tag = "2")]
    pub uuid: String,
    #[prost(bytes = "vec", tag = "3")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "4")]
    pub relay_server: String,
    #[prost(bool, tag = "5")]
    pub secure: bool,
    #[prost(string, tag = "6")]
    pub licence_key: String,
    #[prost(enumeration = "ConnType", tag = "7")]
    pub conn_type: i32,
    #[prost(string, tag = "8")]
    pub token: String,
    #[prost(message, optional, tag = "9")]
    pub control_permissions: Option<ControlPermissions>,
    #[prost(message, optional, tag = "10")]
    pub controlled_context: Option<ControlledContext>,
    #[prost(string, tag = "11")]
    pub switch_code: String,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Enumeration)]
#[repr(i32)]
pub enum ConnType {
    DefaultConn = 0,
    FileTransfer = 1,
    PortForward = 2,
    Rdp = 3,
    ViewCamera = 4,
    Terminal = 5,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct RelayResponse {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "2")]
    pub uuid: String,
    #[prost(string, tag = "3")]
    pub relay_server: String,
    #[prost(oneof = "relay_response::Union", tags = "4, 5")]
    pub union: Option<relay_response::Union>,
    #[prost(string, tag = "6")]
    pub refuse_reason: String,
    #[prost(string, tag = "7")]
    pub version: String,
    #[prost(int32, tag = "9")]
    pub feedback: i32,
    #[prost(bytes = "vec", tag = "10")]
    pub socket_addr_v6: Vec<u8>,
    #[prost(int32, tag = "11")]
    pub upnp_port: i32,
    #[prost(string, tag = "12")]
    pub webrtc_sdp_answer: String,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct PunchHoleResponse {
    #[prost(bytes = "vec", tag = "1")]
    pub socket_addr: Vec<u8>,
    #[prost(bytes = "vec", tag = "2")]
    pub pk: Vec<u8>,
    #[prost(int32, tag = "3")]
    pub failure: i32,
    #[prost(string, tag = "4")]
    pub relay_server: String,
    #[prost(oneof = "punch_hole_response::Union", tags = "5, 6")]
    pub union: Option<punch_hole_response::Union>,
    #[prost(string, tag = "7")]
    pub other_failure: String,
    #[prost(int32, tag = "8")]
    pub feedback: i32,
    #[prost(bool, tag = "9")]
    pub is_udp: bool,
    #[prost(int32, tag = "10")]
    pub upnp_port: i32,
    #[prost(bytes = "vec", tag = "11")]
    pub socket_addr_v6: Vec<u8>,
    #[prost(string, tag = "12")]
    pub webrtc_sdp_answer: String,
}

pub mod punch_hole_response {
    use super::{NatType, Oneof};

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(enumeration = "NatType", tag = "5")]
        NatType(i32),
        #[prost(bool, tag = "6")]
        IsLocal(bool),
    }
}

pub mod relay_response {
    use super::Oneof;

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(string, tag = "4")]
        Id(String),
        #[prost(bytes = "vec", tag = "5")]
        Pk(Vec<u8>),
    }
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct ConfigUpdate {
    #[prost(int32, tag = "1")]
    pub serial: i32,
    #[prost(string, repeated, tag = "2")]
    pub rendezvous_servers: Vec<String>,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct KeyExchange {
    #[prost(bytes = "vec", repeated, tag = "1")]
    pub keys: Vec<Vec<u8>>,
    #[prost(uint32, tag = "2")]
    pub version: u32,
    #[prost(bytes = "vec", tag = "3")]
    pub signed_params: Vec<u8>,
}
#[derive(Clone, PartialEq, ProstMessage)]
pub struct KxParams {
    #[prost(bytes = "vec", tag = "1")]
    pub pk: Vec<u8>,
    #[prost(uint32, tag = "2")]
    pub version: u32,
}
#[derive(Clone, PartialEq, ProstMessage)]
pub struct IceCandidate {
    #[prost(string, tag = "1")]
    pub id: String,
    #[prost(bytes = "vec", tag = "2")]
    pub socket_addr: Vec<u8>,
    #[prost(string, tag = "3")]
    pub session_key: String,
    #[prost(string, tag = "4")]
    pub candidate: String,
}

#[derive(Clone, PartialEq, ProstMessage)]
pub struct RendezvousMessage {
    #[prost(
        oneof = "rendezvous_message::Union",
        tags = "6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 18, 19, 25, 29"
    )]
    pub union: Option<rendezvous_message::Union>,
}

pub mod rendezvous_message {
    use super::{
        ConfigUpdate, FetchLocalAddr, IceCandidate, KeyExchange, LocalAddr, Oneof, PunchHole,
        PunchHoleRequest, PunchHoleResponse, PunchHoleSent, RegisterPeer, RegisterPeerResponse,
        RegisterPk, RegisterPkResponse, RelayResponse, RequestRelay,
    };

    #[derive(Clone, PartialEq, Oneof)]
    pub enum Union {
        #[prost(message, tag = "6")]
        RegisterPeer(RegisterPeer),
        #[prost(message, tag = "7")]
        RegisterPeerResponse(RegisterPeerResponse),
        #[prost(message, tag = "8")]
        PunchHoleRequest(PunchHoleRequest),
        #[prost(message, tag = "9")]
        PunchHole(PunchHole),
        #[prost(message, tag = "10")]
        PunchHoleSent(PunchHoleSent),
        #[prost(message, tag = "11")]
        PunchHoleResponse(PunchHoleResponse),
        #[prost(message, tag = "12")]
        FetchLocalAddr(FetchLocalAddr),
        #[prost(message, tag = "13")]
        LocalAddr(LocalAddr),
        #[prost(message, tag = "14")]
        ConfigureUpdate(ConfigUpdate),
        #[prost(message, tag = "15")]
        RegisterPk(RegisterPk),
        #[prost(message, tag = "16")]
        RegisterPkResponse(RegisterPkResponse),
        #[prost(message, tag = "18")]
        RequestRelay(RequestRelay),
        #[prost(message, tag = "19")]
        RelayResponse(RelayResponse),
        #[prost(message, tag = "25")]
        KeyExchange(KeyExchange),
        #[prost(message, tag = "29")]
        IceCandidate(IceCandidate),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reads_upstream_150_key_exchange_version_and_legacy_absence() {
        for (wire, version) in [
            (
                include_bytes!("../tests/fixtures/rustdesk-1.5-public-key-v0.bin").as_slice(),
                0,
            ),
            (
                include_bytes!("../tests/fixtures/rustdesk-1.5-public-key-v1.bin").as_slice(),
                1,
            ),
        ] {
            let key = PublicKey::decode(wire).unwrap();
            assert_eq!(key.asymmetric_value, b"controller-key");
            assert_eq!(key.symmetric_value, b"sealed-session-key");
            assert_eq!(key.kx_version, version);
            assert_eq!(key.encode_to_vec(), wire);
        }
    }
}

#[cfg(test)]
mod webrtc_wire_tests {
    use super::*;
    #[test]
    fn ice_candidate_matches_150_canonical_field_numbers() {
        // rustdesk/hbb_common 229b904, rendezvous.proto IceCandidate fields 1..4.
        let bytes = [0x0a, 1, b'x', 0x12, 1, 0xab, 0x1a, 1, b'k', 0x22, 1, b'c'];
        let candidate = IceCandidate::decode(bytes.as_slice()).unwrap();
        assert_eq!(candidate.id, "x");
        assert_eq!(candidate.socket_addr, vec![0xab]);
        assert_eq!(candidate.session_key, "k");
        assert_eq!(candidate.candidate, "c");
        assert_eq!(candidate.encode_to_vec(), bytes);
        let wire = RendezvousMessage {
            union: Some(rendezvous_message::Union::IceCandidate(candidate)),
        }
        .encode_to_vec();
        assert_eq!(&wire[..3], &[0xea, 1, 12]); // canonical oneof tag 29
    }
}

#[cfg(test)]
mod client_option_wire_tests {
    use super::*;
    #[test]
    fn view_only_option_matches_canonical_150_misc_and_bool_tags() {
        let wire = [0x9a, 0x01, 0x04, 0x3a, 0x02, 0x60, 0x02];
        let message = Message::decode(wire.as_slice()).unwrap();
        let Some(message::Union::Misc(Misc {
            union: Some(misc::Union::Option(option)),
        })) = message.union
        else {
            panic!("not a client option")
        };
        assert_eq!(option.disable_keyboard, 2);
    }
}

#[cfg(test)]
mod direct_wire_tests {
    use super::*;
    #[test]
    fn local_addr_and_permissions_have_canonical_150_tags() {
        // LocalAddr 1..6, RendezvousMessage.LocalAddr 13, FetchLocalAddr permissions 4.
        let bytes = [
            0x0a, 1, b'a', 0x12, 1, b'b', 0x1a, 1, b'c', 0x22, 1, b'd', 0x2a, 1, b'e', 0x32, 1,
            b'f',
        ];
        let addr = LocalAddr::decode(bytes.as_slice()).unwrap();
        assert_eq!(addr.local_addr, b"b");
        assert_eq!(addr.encode_to_vec(), bytes);
        let msg = RendezvousMessage {
            union: Some(rendezvous_message::Union::LocalAddr(addr)),
        }
        .encode_to_vec();
        assert_eq!(&msg[..2], &[0x6a, 18]);
        let req = FetchLocalAddr::decode([0x22, 2, 0x08, 0x01].as_slice()).unwrap();
        assert_eq!(req.control_permissions.unwrap().permissions, 1);
    }
}
