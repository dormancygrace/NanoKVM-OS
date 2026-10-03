// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::{io, net::IpAddr, path::Path};

use base64::{engine::general_purpose::STANDARD, Engine as _};
use serde::{Deserialize, Serialize};

// RustDesk upstream hbb_common/src/config.rs, commit 69cea8d.
const OFFICIAL_RENDEZVOUS_SERVER: &str = "rs-ny.rustdesk.com:21116";
const OFFICIAL_SERVER_KEY: &str = "OeVuKk5nlHiXp+APNn0Y3pC1Iwpwn44JGqrQCsWqmBw=";

fn enabled() -> bool {
    false
}

fn listen_address() -> String {
    "127.0.0.1".to_owned()
}

fn port() -> u16 {
    21118
}

fn media_socket() -> String {
    "/run/nanokvm-rustdesk/media.sock".to_owned()
}

fn admin_socket() -> String {
    "/run/nanokvm-rustdesk/control.sock".to_owned()
}
fn audio_socket() -> String {
    "/run/nanokvm-rustdesk/audio.sock".to_owned()
}

fn maximum_clients() -> usize {
    1
}

fn frame_rate() -> u32 {
    60
}

fn codec() -> String {
    "auto".to_owned()
}

fn rustdesk_id() -> String {
    "Direct IP only (not registered)".to_owned()
}

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum PasswordMode {
    #[default]
    Permanent,
    Temporary,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(default, deny_unknown_fields)]
pub struct Config {
    #[serde(default = "enabled")]
    pub service_enabled: bool,
    #[serde(default = "listen_address")]
    pub listen_address: String,
    #[serde(default = "port")]
    pub port: u16,
    pub access_control_enabled: bool,
    pub password: String,
    pub password_mode: PasswordMode,
    #[serde(default = "maximum_clients")]
    pub max_clients: usize,
    #[serde(default = "frame_rate")]
    pub fps: u32,
    // Retained for old config files; the shared encoder is authoritative.
    #[serde(default = "codec")]
    pub codec: String,
    #[serde(default = "rustdesk_id")]
    pub rustdesk_id: String,
    pub use_official_id_server: bool,
    pub webrtc_enabled: bool,
    pub rendezvous_server: String,
    pub relay_server: String,
    pub server_key: String,
    #[serde(default = "media_socket")]
    pub media_socket: String,
    #[serde(default = "admin_socket")]
    pub admin_socket: String,
    #[serde(default = "audio_socket")]
    pub audio_socket: String,
}

impl Default for Config {
    fn default() -> Self {
        Self {
            service_enabled: enabled(),
            listen_address: listen_address(),
            port: port(),
            access_control_enabled: true,
            password: String::new(),
            password_mode: PasswordMode::Permanent,
            max_clients: maximum_clients(),
            fps: frame_rate(),
            codec: codec(),
            rustdesk_id: rustdesk_id(),
            use_official_id_server: true,
            webrtc_enabled: false,
            rendezvous_server: String::new(),
            relay_server: String::new(),
            server_key: String::new(),
            media_socket: media_socket(),
            admin_socket: admin_socket(),
            audio_socket: audio_socket(),
        }
    }
}

impl Config {
    pub fn load(path: &Path) -> io::Result<Self> {
        let data = std::fs::read(path)?;
        serde_json::from_slice(&data).map_err(|error| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                format!("decode {}: {error}", path.display()),
            )
        })
    }

    pub fn validate(&self) -> Result<(), String> {
        if !self.access_control_enabled {
            return Err("password authentication is mandatory".to_owned());
        }
        self.listen_address
            .parse::<IpAddr>()
            .map_err(|_| "listen_address must be an IPv4 or IPv6 address".to_owned())?;
        if self.port == 0 {
            return Err("port must be between 1 and 65535".to_owned());
        }
        if self.max_clients == 0 || self.max_clients > 8 {
            return Err("max_clients must be between 1 and 8".to_owned());
        }
        if self.fps == 0 || self.fps > 60 {
            return Err("fps must be between 1 and 60".to_owned());
        }
        if self.codec != "auto" && self.codec != "h264" && self.codec != "h265" {
            return Err("codec must be auto, h264 or h265".to_owned());
        }
        if self.rustdesk_id.trim().is_empty() {
            return Err("rustdesk_id must not be empty".to_owned());
        }
        for (name, value) in [
            ("rendezvous_server", &self.rendezvous_server),
            ("relay_server", &self.relay_server),
            ("server_key", &self.server_key),
        ] {
            if value.len() > 255 || value.chars().any(char::is_control) {
                return Err(format!(
                    "{name} must contain at most 255 printable characters"
                ));
            }
        }
        if !self.server_key.is_empty()
            && STANDARD
                .decode(&self.server_key)
                .map(|key| key.len() != 32)
                .unwrap_or(true)
        {
            return Err("server_key must be a base64-encoded 32-byte public key".to_owned());
        }
        if (self.password_mode == PasswordMode::Permanent || !self.password.is_empty())
            && !(8..=64).contains(&self.password.len())
        {
            return Err(
                "password must contain 8 to 64 bytes when access control is enabled".to_owned(),
            );
        }
        if !Path::new(&self.media_socket).is_absolute() {
            return Err("media_socket must be absolute".to_owned());
        }
        if !Path::new(&self.admin_socket).is_absolute() {
            return Err("admin_socket must be absolute".to_owned());
        }
        Ok(())
    }

    pub fn apply_server_mode(&mut self) {
        if self.use_official_id_server {
            self.rendezvous_server = OFFICIAL_RENDEZVOUS_SERVER.to_owned();
            self.relay_server.clear();
            self.server_key = OFFICIAL_SERVER_KEY.to_owned();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{Config, PasswordMode, OFFICIAL_RENDEZVOUS_SERVER, OFFICIAL_SERVER_KEY};

    #[test]
    fn webrtc_is_opt_in_for_existing_and_new_configs() {
        assert!(!Config::default().webrtc_enabled);
        let old: Config = serde_json::from_str(r#"{"password":"stored-pass"}"#).unwrap();
        assert!(!old.webrtc_enabled);
        let opted: Config =
            serde_json::from_str(r#"{"password":"stored-pass","webrtc_enabled":true}"#).unwrap();
        assert!(opted.webrtc_enabled);
    }

    #[test]
    fn defaults_require_a_password() {
        let error = Config::default().validate().unwrap_err();
        assert!(error.contains("password"));
    }

    #[test]
    fn temporary_mode_allows_generation_without_stored_password() {
        let config = Config {
            password_mode: PasswordMode::Temporary,
            ..Config::default()
        };
        config.validate().unwrap();
        let legacy: Config = serde_json::from_str(r#"{"password":"stored-pass"}"#).unwrap();
        assert_eq!(legacy.password_mode, PasswordMode::Permanent);
        assert!(serde_json::from_str::<Config>(r#"{"password_mode":"unknown"}"#).is_err());
    }

    #[test]
    fn valid_direct_access_config() {
        let config = Config {
            password: "onekvm-test".to_owned(),
            rendezvous_server: "test.invalid:21116".to_owned(),
            ..Config::default()
        };
        config.validate().unwrap();
    }

    #[test]
    fn official_server_mode_uses_upstream_defaults() {
        let mut config = Config {
            use_official_id_server: true,
            rendezvous_server: "custom.example:21116".to_owned(),
            relay_server: "custom.example:21117".to_owned(),
            server_key: STANDARD_KEY.to_owned(),
            password: "onekvm-test".to_owned(),
            ..Config::default()
        };
        config.apply_server_mode();
        assert_eq!(config.rendezvous_server, OFFICIAL_RENDEZVOUS_SERVER);
        assert!(config.relay_server.is_empty());
        assert_eq!(config.server_key, OFFICIAL_SERVER_KEY);
        config.validate().unwrap();
    }

    const STANDARD_KEY: &str = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
}
