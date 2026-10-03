// SPDX-License-Identifier: AGPL-3.0-only
use rand::{rngs::OsRng, RngCore};
use std::{
    fs::{self, OpenOptions},
    io::{self, Write},
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
};

// Ten easy-to-read characters from a 32-character alphabet: 50 random bits.
const ALPHABET: &[u8; 32] = b"ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
pub const PATH: &str = "/run/nanokvm-rustdesk/temporary-password";

pub fn generate() -> io::Result<String> {
    let mut random = [0_u8; 10];
    OsRng
        .try_fill_bytes(&mut random)
        .map_err(io::Error::other)?;
    Ok(random
        .iter()
        .map(|byte| ALPHABET[(byte & 31) as usize] as char)
        .collect())
}

pub struct Guard(PathBuf);
impl Guard {
    pub fn publish(path: &Path, password: &str) -> io::Result<Self> {
        let temporary = path.with_extension("tmp");
        let result = (|| {
            let mut file = OpenOptions::new()
                .write(true)
                .create(true)
                .truncate(true)
                .mode(0o600)
                .open(&temporary)?;
            file.set_permissions(fs::Permissions::from_mode(0o600))?;
            file.write_all(password.as_bytes())?;
            file.sync_all()?;
            fs::rename(&temporary, path)
        })();
        if result.is_err() {
            let _ = fs::remove_file(&temporary);
        }
        result?;
        Ok(Self(path.to_owned()))
    }
}
impl Drop for Guard {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn generated_password_is_private_and_removed_on_stop() {
        let password = generate().unwrap();
        assert_eq!(password.len(), 10);
        assert!(password.bytes().all(|byte| ALPHABET.contains(&byte)));
        let dir = std::env::temp_dir().join(format!("nanokvm-password-{}", std::process::id()));
        fs::create_dir_all(&dir).unwrap();
        let path = dir.join("password");
        let guard = Guard::publish(&path, &password).unwrap();
        assert_eq!(fs::read_to_string(&path).unwrap(), password);
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        drop(guard);
        assert!(!path.exists());
        fs::remove_dir(dir).unwrap();
    }
}
