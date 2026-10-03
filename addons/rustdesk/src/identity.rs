// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::{
    env, fs, io,
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
};

use base64::{engine::general_purpose::STANDARD, Engine as _};
use ed25519_dalek::{Signer, SigningKey};
use rand::{distributions::Alphanumeric, Rng, RngCore};
use serde::{Deserialize, Serialize};

const IDENTITY_FILE: &str = "identity.json";
const SETTINGS_OUTPUT_FILE: &str = "settings-output.json";

#[derive(Clone)]
pub struct RustDeskIdentity {
    pub id: String,
    pub uuid: Vec<u8>,
    pub password_salt: String,
    signing_key: SigningKey,
}

#[derive(Deserialize, Serialize)]
struct StoredIdentity {
    id: String,
    uuid: String,
    signing_seed: String,
    #[serde(default)]
    password_salt: String,
}

impl RustDeskIdentity {
    pub fn load() -> io::Result<Self> {
        let root = env::var_os("NANOKVM_RUSTDESK_DATA")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from("/etc/nanokvm-rustdesk"));
        Self::load_from(&root)
    }

    pub fn load_from(root: &Path) -> io::Result<Self> {
        fs::create_dir_all(root)?;
        let path = root.join(IDENTITY_FILE);
        match fs::read(&path) {
            Ok(data) => {
                let mut identity = Self::decode(&data)?;
                if identity.password_salt.len() != 16 {
                    identity.password_salt = random_password_salt();
                    identity.store(&path)?;
                }
                Ok(identity)
            }
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                let identity = Self::generate();
                identity.store(&path)?;
                Ok(identity)
            }
            Err(error) => Err(error),
        }
    }

    pub fn public_key(&self) -> [u8; 32] {
        self.signing_key.verifying_key().to_bytes()
    }

    /// Sodium's `crypto_sign` wire format is the 64-byte Ed25519 signature
    /// followed by the signed message.
    pub fn sign(&self, message: &[u8]) -> Vec<u8> {
        let signature = self.signing_key.sign(message);
        let mut signed = Vec::with_capacity(64 + message.len());
        signed.extend_from_slice(&signature.to_bytes());
        signed.extend_from_slice(message);
        signed
    }

    pub fn publish_settings_output(&self) -> io::Result<()> {
        let root = env::var_os("NANOKVM_RUSTDESK_DATA")
            .map(PathBuf::from)
            .unwrap_or_else(|| PathBuf::from("/etc/nanokvm-rustdesk"));
        self.publish_settings_output_to(&root)
    }

    fn publish_settings_output_to(&self, root: &Path) -> io::Result<()> {
        let data = serde_json::to_vec_pretty(&serde_json::json!({
            "rustdesk_id": self.id,
        }))
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        write_file(&root.join(SETTINGS_OUTPUT_FILE), &data, 0o600)
    }

    fn generate() -> Self {
        let mut random = rand::thread_rng();
        let id = random
            .next_u32()
            .wrapping_rem(1_000_000_000)
            .saturating_add(1_000_000_000)
            .to_string();
        let mut uuid = vec![0_u8; 16];
        random.fill_bytes(&mut uuid);
        let mut seed = [0_u8; 32];
        random.fill_bytes(&mut seed);
        Self {
            id,
            uuid,
            password_salt: random_password_salt(),
            signing_key: SigningKey::from_bytes(&seed),
        }
    }

    fn decode(data: &[u8]) -> io::Result<Self> {
        let stored: StoredIdentity = serde_json::from_slice(data)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if stored.id.len() != 10 || !stored.id.bytes().all(|byte| byte.is_ascii_digit()) {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "stored RustDesk ID is not a 10-digit number",
            ));
        }
        let uuid = STANDARD
            .decode(stored.uuid)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        if uuid.len() != 16 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "stored RustDesk UUID must be 16 bytes",
            ));
        }
        let seed = STANDARD
            .decode(stored.signing_seed)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        let seed: [u8; 32] = seed.try_into().map_err(|_| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                "stored RustDesk signing seed must be 32 bytes",
            )
        })?;
        Ok(Self {
            id: stored.id,
            uuid,
            password_salt: stored.password_salt,
            signing_key: SigningKey::from_bytes(&seed),
        })
    }

    fn store(&self, path: &Path) -> io::Result<()> {
        let stored = StoredIdentity {
            id: self.id.clone(),
            uuid: STANDARD.encode(&self.uuid),
            signing_seed: STANDARD.encode(self.signing_key.to_bytes()),
            password_salt: self.password_salt.clone(),
        };
        let data = serde_json::to_vec_pretty(&stored)
            .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
        write_file(path, &data, 0o600)
    }
}

fn random_password_salt() -> String {
    rand::thread_rng()
        .sample_iter(Alphanumeric)
        .take(16)
        .map(char::from)
        .collect()
}

fn write_file(path: &Path, data: &[u8], mode: u32) -> io::Result<()> {
    let temporary = path.with_extension(format!("tmp-{}", std::process::id()));
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true).mode(mode);
    let mut file = options.open(&temporary)?;
    use std::io::Write as _;
    file.write_all(data)?;
    file.write_all(b"\n")?;
    file.sync_all()?;
    drop(file);
    fs::rename(&temporary, path)?;
    fs::set_permissions(path, fs::Permissions::from_mode(mode))
}

#[cfg(test)]
mod tests {
    use std::time::{SystemTime, UNIX_EPOCH};

    use ed25519_dalek::{Signature, Verifier, VerifyingKey};

    use std::os::unix::fs::PermissionsExt;

    use super::RustDeskIdentity;

    #[test]
    fn identity_is_stable_and_signatures_are_sodium_compatible() {
        let root = std::env::temp_dir().join(format!(
            "onekvm-rustdesk-identity-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let first = RustDeskIdentity::load_from(&root).unwrap();
        let second = RustDeskIdentity::load_from(&root).unwrap();
        assert_eq!(first.id, second.id);
        assert_eq!(first.uuid, second.uuid);
        assert_eq!(first.public_key(), second.public_key());
        assert_eq!(first.password_salt, second.password_salt);
        assert_eq!(first.password_salt.len(), 16);

        first.publish_settings_output_to(&root).unwrap();
        let output: serde_json::Value =
            serde_json::from_slice(&std::fs::read(root.join("settings-output.json")).unwrap())
                .unwrap();
        assert_eq!(output["rustdesk_id"], first.id);
        assert_eq!(
            std::fs::metadata(root.join("settings-output.json"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );
        assert_eq!(
            std::fs::metadata(root.join("identity.json"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );

        let message = b"OneKVM RustDesk identity";
        let signed = first.sign(message);
        let signature = Signature::from_slice(&signed[..64]).unwrap();
        VerifyingKey::from_bytes(&first.public_key())
            .unwrap()
            .verify(&signed[64..], &signature)
            .unwrap();
        std::fs::remove_dir_all(root).unwrap();
    }
}
