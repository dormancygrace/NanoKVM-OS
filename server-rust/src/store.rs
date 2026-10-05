use crate::{crypto, Error};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::Write,
    os::unix::fs::OpenOptionsExt,
    path::{Path, PathBuf},
    sync::Mutex,
};

#[derive(Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct User {
    pub username: String,
    #[serde(rename = "password")]
    pub hash: String,
    pub role: String,
    pub enabled: bool,
    #[serde(default)]
    pub token_version: u64,
    #[serde(default, skip_serializing_if = "is_false")]
    pub must_change_password: bool,
    #[serde(default, skip_serializing_if = "is_false")]
    pub system_account: bool,
}
fn is_false(b: &bool) -> bool {
    !*b
}
impl User {
    pub fn info(&self) -> serde_json::Value {
        let mut v =
            serde_json::json!({"username":self.username,"role":self.role,"enabled":self.enabled});
        if self.system_account {
            v["systemAccount"] = true.into();
        }
        v
    }
}
#[derive(Clone, Serialize, Deserialize)]
pub struct Database {
    pub version: u32,
    pub users: Vec<User>,
    #[serde(default)]
    pub username: String,
    #[serde(default)]
    pub password: String,
}
#[derive(Default, Deserialize)]
pub struct Patch {
    pub username: Option<String>,
    pub role: Option<String>,
    pub enabled: Option<bool>,
}
pub struct Store {
    pub path: PathBuf,
    lock: Mutex<()>,
    default_hash: Mutex<Option<String>>,
}

pub fn atomic_write(path: &Path, data: &[u8], mode: u32) -> Result<(), Error> {
    let parent = path.parent().ok_or("missing parent directory")?;
    fs::create_dir_all(parent)?;
    let mut entropy = [0u8; 16];
    getrandom::fill(&mut entropy)?;
    let suffix: String = entropy.iter().map(|b| format!("{b:02x}")).collect();
    let temp = parent.join(format!(".v3-{}-{suffix}", std::process::id()));
    let result = (|| -> Result<(), Error> {
        let mut file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(mode)
            .open(&temp)?;
        // Match Go atomicfile.Chmod even under a restrictive process umask.
        file.set_permissions(std::os::unix::fs::PermissionsExt::from_mode(mode))?;
        file.write_all(data)?;
        file.sync_all()?;
        drop(file);
        fs::rename(&temp, path)?;
        fs::File::open(parent)?.sync_all()?;
        Ok(())
    })();
    let _ = fs::remove_file(&temp);
    result
}

fn new_version() -> Result<u64, Error> {
    loop {
        let mut b = [0u8; 8];
        getrandom::fill(&mut b)?;
        let v = u64::from_le_bytes(b);
        if v != 0 && v != u64::MAX {
            return Ok(v);
        }
    }
}
pub fn valid_username(s: &str) -> Result<(), Error> {
    if s.is_empty()
        || s.len() > 32
        || !s.as_bytes()[0].is_ascii_alphanumeric()
        || !s
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_.-".contains(&b))
    {
        return Err(
            "username must be 1-32 characters and contain only letters, numbers, '.', '_' or '-'"
                .into(),
        );
    }
    Ok(())
}
pub fn valid_password(s: &str) -> Result<(), Error> {
    if !(8..=72).contains(&s.len()) {
        Err("password must be between 8 and 72 bytes".into())
    } else {
        Ok(())
    }
}
fn matches(hash: &str, password: &str) -> bool {
    bcrypt::verify(password, hash).unwrap_or(false)
        || crypto::decrypt(hash).is_ok_and(|v| v == password)
}
impl Database {
    fn validate(&mut self) -> Result<(), Error> {
        if self.version != 1 {
            return Err(format!("unsupported account file version: {}", self.version).into());
        }
        if self.users.is_empty() {
            return Err("account file contains no users".into());
        }
        let mut seen = std::collections::HashSet::new();
        let mut owners = 0;
        for user in &mut self.users {
            valid_username(&user.username)?;
            if user.hash.is_empty() || !["admin", "user"].contains(&user.role.as_str()) {
                return Err("account file contains an invalid user".into());
            }
            if !seen.insert(user.username.clone()) {
                return Err("account file contains duplicate usernames".into());
            }
            if user.token_version == 0 {
                user.token_version = 1;
            }
            if user.system_account {
                owners += 1;
                if user.role != "admin" || !user.enabled || owners > 1 {
                    return Err(
                        "the device owner account must remain an enabled administrator".into(),
                    );
                }
            }
        }
        if !self.users.iter().any(|u| u.role == "admin" && u.enabled) {
            return Err("at least one enabled admin is required".into());
        }
        Ok(())
    }
    fn index(&self, name: &str) -> Result<usize, Error> {
        self.users
            .iter()
            .position(|u| u.username == name)
            .ok_or_else(|| "user not found".into())
    }
}
impl Store {
    pub fn new(path: PathBuf) -> Self {
        Self {
            path,
            lock: Mutex::new(()),
            default_hash: Mutex::new(None),
        }
    }
    fn load(&self, migrate: bool) -> Result<Database, Error> {
        let mut db = match fs::read(&self.path) {
            Ok(data) => {
                let v: serde_json::Value = serde_json::from_slice(&data)?;
                if v.get("version")
                    .is_some_and(|value| !value.is_null() && value != 0)
                {
                    serde_json::from_value::<Database>(v)?
                } else {
                    let name = v["username"].as_str().ok_or("invalid account file")?;
                    let hash = v["password"]
                        .as_str()
                        .filter(|v| !v.is_empty())
                        .ok_or("invalid account file")?;
                    valid_username(name)?;
                    let db = Database {
                        version: 1,
                        username: String::new(),
                        password: String::new(),
                        users: vec![User {
                            username: name.into(),
                            hash: hash.into(),
                            role: "admin".into(),
                            enabled: true,
                            token_version: new_version()?,
                            must_change_password: matches(hash, "admin"),
                            system_account: true,
                        }],
                    };
                    if migrate {
                        self.save(db.clone())?;
                    }
                    db
                }
            }
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                let mut hash = self
                    .default_hash
                    .lock()
                    .map_err(|_| "account store unavailable")?;
                if hash.is_none() {
                    *hash = Some(bcrypt::hash("admin", 10)?);
                }
                let db = Database {
                    version: 1,
                    username: String::new(),
                    password: String::new(),
                    users: vec![User {
                        username: "admin".into(),
                        hash: hash.clone().unwrap(),
                        role: "admin".into(),
                        enabled: true,
                        token_version: new_version()?,
                        must_change_password: true,
                        system_account: true,
                    }],
                };
                if migrate {
                    self.save(db.clone())?;
                }
                db
            }
            Err(e) => return Err(e.into()),
        };
        db.validate()?;
        Ok(db)
    }
    fn save(&self, mut db: Database) -> Result<(), Error> {
        db.validate()?;
        let owner = db
            .users
            .iter()
            .find(|u| u.system_account)
            .or_else(|| db.users.iter().find(|u| u.role == "admin" && u.enabled))
            .ok_or("missing owner")?;
        db.username = owner.username.clone();
        db.password = owner.hash.clone();
        let mut data = serde_json::to_vec_pretty(&db)?;
        data.push(b'\n');
        atomic_write(&self.path, &data, 0o600)
    }
    pub fn list(&self) -> Result<Vec<User>, Error> {
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        Ok(self.load(false)?.users)
    }
    pub fn get(&self, name: &str) -> Result<User, Error> {
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        let db = self.load(false)?;
        Ok(db.users[db.index(name)?].clone())
    }
    pub fn authenticate(&self, name: &str, password: &str) -> Result<Option<User>, Error> {
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        let mut db = self.load(true)?;
        let Ok(index) = db.index(name) else {
            return Ok(None);
        };
        if !db.users[index].enabled || !matches(&db.users[index].hash, password) {
            return Ok(None);
        }
        if !bcrypt::verify(password, &db.users[index].hash).unwrap_or(false) {
            db.users[index].hash = bcrypt::hash(password, 10)?;
            self.save(db.clone())?;
        }
        Ok(Some(db.users[index].clone()))
    }
    pub fn create(&self, name: &str, password: &str, role: &str) -> Result<(), Error> {
        valid_username(name)?;
        valid_password(password)?;
        if !["admin", "user"].contains(&role) {
            return Err("invalid role".into());
        }
        let hash = bcrypt::hash(password, 10)?;
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        let mut db = self.load(true)?;
        if db.index(name).is_ok() {
            return Err("username already exists".into());
        }
        db.users.push(User {
            username: name.into(),
            hash,
            role: role.into(),
            enabled: true,
            token_version: new_version()?,
            must_change_password: false,
            system_account: false,
        });
        self.save(db)
    }
    pub fn update(&self, actor: &str, name: &str, patch: Patch) -> Result<bool, Error> {
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        let mut db = self.load(true)?;
        let index = db.index(name)?;
        let mut user = db.users[index].clone();
        if let Some(ref new_name) = patch.username {
            valid_username(new_name)?;
            if new_name != name && db.index(new_name).is_ok() {
                return Err("username already exists".into());
            }
        }
        if user.system_account
            && patch.username.as_ref().is_some_and(|n| n != name)
            && actor != name
        {
            return Err("only the device owner can rename the device owner account".into());
        }
        if patch
            .role
            .as_ref()
            .is_some_and(|r| r != "admin" && r != "user")
        {
            return Err("invalid role".into());
        }
        let demoting =
            patch.role.as_ref().is_some_and(|r| r != "admin") || patch.enabled == Some(false);
        if actor == name && user.role == "admin" && demoting {
            return Err("administrators cannot disable or demote themselves".into());
        }
        if user.system_account && demoting {
            return Err("the device owner account must remain an enabled administrator".into());
        }
        let changed = patch.username.as_ref().is_some_and(|n| n != &user.username)
            || patch.role.as_ref().is_some_and(|r| r != &user.role)
            || patch.enabled.is_some_and(|b| b != user.enabled);
        if !changed {
            return Ok(false);
        }
        if let Some(v) = patch.username {
            user.username = v;
        }
        if let Some(v) = patch.role {
            user.role = v;
        }
        if let Some(v) = patch.enabled {
            user.enabled = v;
        }
        user.token_version = user.token_version.wrapping_add(1);
        db.users[index] = user;
        self.save(db)?;
        Ok(true)
    }
    pub fn delete(&self, actor: &str, name: &str) -> Result<(), Error> {
        if actor == name {
            return Err("administrators cannot delete themselves".into());
        }
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        let mut db = self.load(true)?;
        let i = db.index(name)?;
        if db.users[i].system_account {
            return Err("the device owner account must remain an enabled administrator".into());
        }
        db.users.remove(i);
        self.save(db)
    }
    pub fn revoke(&self, name: &str) -> Result<(), Error> {
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        let mut db = self.load(true)?;
        let i = db.index(name)?;
        db.users[i].token_version = db.users[i].token_version.wrapping_add(1);
        self.save(db)
    }
    pub fn password(
        &self,
        name: &str,
        password: &str,
        after: impl FnOnce() -> Result<(), Error>,
    ) -> Result<(), Error> {
        valid_password(password)?;
        let hash = bcrypt::hash(password, 10)?;
        let _guard = self.lock.lock().map_err(|_| "account store unavailable")?;
        let mut db = self.load(true)?;
        let i = db.index(name)?;
        let previous = db.clone();
        db.users[i].hash = hash;
        db.users[i].must_change_password = false;
        db.users[i].token_version = db.users[i].token_version.wrapping_add(1);
        self.save(db)?;
        if let Err(error) = after() {
            self.save(previous).map_err(|e| {
                format!("system password update failed: {error}; account rollback failed: {e}")
            })?;
            return Err(error);
        }
        Ok(())
    }
}

#[cfg(test)]
mod atomic_mode_tests {
    #[test]
    fn requested_file_permissions_survive_restrictive_process_umask() {
        const FLAG: &str = "NK_V3_ATOMIC_MODE_CHILD";
        if std::env::var_os(FLAG).is_none() {
            let exe = std::env::current_exe().unwrap();
            let runner = if cfg!(target_arch = "riscv64") {
                std::env::var_os("CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER")
            } else {
                None
            };
            let mut command = match runner {
                Some(runner) => {
                    let mut command = std::process::Command::new(runner);
                    command.arg(exe);
                    command
                }
                None => std::process::Command::new(exe),
            };
            let result=command.args(["--exact","store::atomic_mode_tests::requested_file_permissions_survive_restrictive_process_umask"]).env(FLAG,"1").output().unwrap();
            assert!(
                result.status.success(),
                "{}",
                String::from_utf8_lossy(&result.stderr)
            );
            return;
        }
        // Child-only umask: never race the other test threads' file creation.
        unsafe {
            libc::umask(0o077);
        }
        use std::os::unix::fs::PermissionsExt;
        let temp = tempfile::tempdir().unwrap();
        let path = temp.path().join("script");
        for mode in [0o755, 0o644, 0o600] {
            super::atomic_write(&path, b"complete contents", mode).unwrap();
            assert_eq!(
                std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
                mode
            );
            assert_eq!(std::fs::read(&path).unwrap(), b"complete contents");
        }
    }
}
