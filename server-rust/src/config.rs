use crate::{store::atomic_write, Error};
use base64::{engine::general_purpose::URL_SAFE, Engine};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    path::{Path, PathBuf},
};

#[derive(Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct Config {
    pub proto: String,
    pub host: String,
    pub port: Port,
    pub cert: Cert,
    pub logger: Logger,
    pub authentication: String,
    pub jwt: Jwt,
    pub stun: String,
    pub turn: Turn,
    pub security: Security,
    pub alpine: Alpine,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct Logger {
    pub level: String,
    pub file: String,
}
impl Default for Logger {
    fn default() -> Self {
        Self {
            level: "info".into(),
            file: "stdout".into(),
        }
    }
}
#[derive(Clone, Default, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Turn {
    #[serde(alias = "turnaddr")]
    pub turn_addr: String,
    #[serde(alias = "turnuser")]
    pub turn_user: String,
    #[serde(alias = "turncred")]
    pub turn_cred: String,
}
#[derive(Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Alpine {
    #[serde(rename = "builderURL", alias = "builderurl")]
    pub builder_url: String,
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct Port {
    pub http: u16,
    pub https: u16,
}
impl Default for Port {
    fn default() -> Self {
        Self {
            http: 80,
            https: 443,
        }
    }
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct Cert {
    pub crt: String,
    pub key: String,
}
impl Default for Cert {
    fn default() -> Self {
        Self {
            crt: "/etc/kvm/server.crt".into(),
            key: "/etc/kvm/server.key".into(),
        }
    }
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Jwt {
    #[serde(alias = "secretkey")]
    pub secret_key: String,
    #[serde(alias = "refreshtokenduration")]
    pub refresh_token_duration: u64,
    #[serde(alias = "revoketokensonlogout")]
    pub revoke_tokens_on_logout: bool,
}
impl Default for Jwt {
    fn default() -> Self {
        Self {
            secret_key: String::new(),
            refresh_token_duration: 2678400,
            revoke_tokens_on_logout: false,
        }
    }
}
#[derive(Clone, Serialize, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct Security {
    #[serde(alias = "loginlockoutduration")]
    pub login_lockout_duration: i64,
    #[serde(alias = "loginmaxfailures")]
    pub login_max_failures: i64,
    #[serde(alias = "trustedproxies")]
    pub trusted_proxies: Vec<String>,
    #[serde(alias = "allowedorigins")]
    pub allowed_origins: Vec<String>,
}
impl Default for Security {
    fn default() -> Self {
        Self {
            login_lockout_duration: 300,
            login_max_failures: 5,
            trusted_proxies: vec!["127.0.0.1/32".into(), "::1/128".into()],
            allowed_origins: vec![],
        }
    }
}
impl Default for Config {
    fn default() -> Self {
        Self {
            proto: "https".into(),
            host: String::new(),
            port: Port::default(),
            cert: Cert::default(),
            logger: Logger::default(),
            authentication: "enable".into(),
            jwt: Jwt {
                revoke_tokens_on_logout: true,
                ..Jwt::default()
            },
            stun: "stun.l.google.com:19302".into(),
            turn: Turn::default(),
            security: Security::default(),
            alpine: Alpine::default(),
        }
    }
}

pub fn rooted(root: &Path, path: &str) -> Result<PathBuf, Error> {
    let path = Path::new(path);
    if !path.is_absolute()
        || path
            .components()
            .any(|c| matches!(c, std::path::Component::ParentDir))
    {
        return Err("configuration paths must be absolute without parent traversal".into());
    }
    Ok(root.join(path.strip_prefix("/")?))
}

impl Config {
    pub fn load(root: &Path) -> Result<Self, Error> {
        let file = rooted(root, "/etc/kvm/server.yaml")?;
        let mut conf: Self = match fs::read(&file) {
            Ok(data) => {
                // Viper matches configuration keys case-insensitively and treats
                // explicit null values as unset. Preserve the original file bytes.
                let yaml: serde_json::Value = serde_saphyr::from_str(std::str::from_utf8(&data)?)?;
                serde_json::from_value(normalize(yaml)?)?
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                let conf = Self::default();
                atomic_write(&file, serde_saphyr::to_string(&conf)?.as_bytes(), 0o600)?;
                conf
            }
            Err(e) => return Err(e.into()),
        };
        if !["https", "http"].contains(&conf.proto.as_str())
            || conf.port.http == 0
            || conf.port.https == 0
            || !["enable", "disable", ""].contains(&conf.authentication.as_str())
        {
            return Err("invalid server configuration; existing file preserved".into());
        }
        if conf.authentication.is_empty() {
            conf.authentication = "enable".into();
        }
        if conf.security.login_max_failures <= 0 {
            conf.security.login_max_failures = 5;
        }
        if conf.stun.is_empty() {
            conf.stun = "stun.l.google.com:19302".into();
        }
        if conf.jwt.refresh_token_duration == 0 {
            conf.jwt.refresh_token_duration = 2678400;
        }
        if conf.jwt.secret_key.is_empty() {
            let secret_file = rooted(root, "/etc/kvm/.jwt_secret")?;
            conf.jwt.secret_key = match fs::read_to_string(&secret_file) {
                Ok(data) if !data.trim().is_empty() => {
                    use std::os::unix::fs::PermissionsExt;
                    fs::set_permissions(&secret_file, fs::Permissions::from_mode(0o600))?;
                    data.trim().into()
                }
                Ok(_) => String::new(),
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => String::new(),
                Err(e) => return Err(e.into()),
            };
            if conf.jwt.secret_key.is_empty() {
                let mut bytes = [0u8; 64];
                getrandom::fill(&mut bytes)?;
                conf.jwt.secret_key = URL_SAFE.encode(bytes);
                atomic_write(
                    &secret_file,
                    format!("{}\n", conf.jwt.secret_key).as_bytes(),
                    0o600,
                )?;
            }
            conf.jwt.revoke_tokens_on_logout = true;
        }
        Ok(conf)
    }
}

fn normalize(value: serde_json::Value) -> Result<serde_json::Value, Error> {
    use serde_json::{Map, Value};
    match value {
        Value::Object(mapping) => {
            let mut normalized = Map::new();
            for (key, value) in mapping {
                let key = key.to_lowercase();
                if normalized.contains_key(&key) {
                    return Err("ambiguous case-insensitive configuration key".into());
                }
                if !value.is_null() {
                    normalized.insert(key, normalize(value)?);
                }
            }
            Ok(Value::Object(normalized))
        }
        other => Ok(other),
    }
}
