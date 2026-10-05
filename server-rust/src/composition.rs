//! Whole-gadget transactions, with exact rollback of marker data and script.
use crate::{fsroot, hid_settings, store::atomic_write, systemops::Action, Error};
use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::{Path, PathBuf},
    time::Duration,
};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Mode {
    Normal,
    HidOnly,
}
impl Mode {
    pub fn parse(value: &str) -> Result<Self, Error> {
        match value {
            "normal" => Ok(Self::Normal),
            "hid-only" => Ok(Self::HidOnly),
            _ => Err("invalid USB mode".into()),
        }
    }
    pub fn name(self) -> &'static str {
        match self {
            Self::Normal => "normal",
            Self::HidOnly => "hid-only",
        }
    }
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Composition {
    pub mode: Mode,
    pub keyboard: bool,
    pub relative: bool,
    pub absolute: bool,
    pub network: bool,
    pub disk: bool,
    pub serial: bool,
    pub audio: bool,
    pub windows_pointer: bool,
}
impl Composition {
    pub fn read(root: &Path) -> Self {
        let mode = if hid_settings::mode(root).is_ok_and(|mode| mode == "hid-only") {
            Mode::HidOnly
        } else {
            Mode::Normal
        };
        let exists =
            |name: &str| fsroot::resolve(root, &Path::new("/boot").join(name), false).is_ok();
        let hid = !exists("disable_hid");
        Self {
            mode,
            keyboard: hid && !exists("usb.disable_keyboard"),
            relative: hid && !exists("usb.disable_relative"),
            absolute: hid && !exists("usb.disable_absolute"),
            network: mode == Mode::Normal && (exists("usb.rndis0") || exists("usb.ncm")),
            disk: mode == Mode::Normal && exists("usb.disk0"),
            serial: exists("usb.acm"),
            audio: mode == Mode::Normal && exists("usb.audio"),
            windows_pointer: exists("usb.pointer_windows"),
        }
    }
    pub fn empty(self) -> bool {
        !(self.keyboard
            || self.relative
            || self.absolute
            || self.network
            || self.disk
            || self.serial
            || self.audio)
    }
    pub fn usage(self) -> (u8, u8) {
        let hid = u8::from(self.keyboard) + u8::from(self.relative) + u8::from(self.absolute);
        (
            hid + 2 * u8::from(self.network) + u8::from(self.disk) + 2 * u8::from(self.serial),
            hid + u8::from(self.network)
                + u8::from(self.disk)
                + u8::from(self.serial)
                + u8::from(self.audio),
        )
    }
    pub fn validate(self) -> Result<(), Error> {
        if self.mode == Mode::HidOnly && (self.network || self.disk || self.audio) {
            return Err("USB network, disk and audio are unavailable in HID-only mode".into());
        }
        let (input, output) = self.usage();
        if input > 6 || output > 7 {
            return Err("USB endpoint budget exceeded".into());
        }
        Ok(())
    }
    pub fn revision(self) -> String {
        Sha256::digest(
            format!(
                "{}:{}:{}:{}:{}:{}:{}:{}:{}",
                self.mode.name(),
                self.keyboard,
                self.relative,
                self.absolute,
                self.network,
                self.disk,
                self.serial,
                self.audio,
                self.windows_pointer
            )
            .as_bytes(),
        )
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
    }

    pub fn response(self, supported: bool) -> Value {
        let (input, output) = self.usage();
        let cost = |input, output| json!({"in":input,"out":output});
        json!({"mode":self.mode.name(),"pointerProfile":if self.windows_pointer {"windows"} else {"default"},"windowsPointerSupported":supported,
            "keyboard":self.keyboard,"relative":self.relative,"absolute":self.absolute,"network":self.network,"disk":self.disk,"serial":self.serial,"audio":self.audio,
            "media":false,"hid":self.keyboard || self.relative || self.absolute,"revision":self.revision(),
            "budget":{"inUsed":input,"outUsed":output,"inLimit":6,"outLimit":7},
            "costs":{"hid":cost(3,3),"keyboard":cost(1,1),"relative":cost(1,1),"absolute":cost(1,1),"network":cost(2,1),"disk":cost(1,1),"serial":cost(2,1),"audio":cost(0,1)}})
    }
    pub fn remote_access_defaults(mut self, audio: bool) -> Self {
        self.keyboard = true;
        self.relative = true;
        self.absolute = true;
        if audio {
            self.mode = Mode::Normal;
            self.audio = true;
        }
        self
    }
    pub fn toggle(mut self, device: &str) -> Self {
        match device {
            "network" => self.network = !self.network,
            "disk" => self.disk = !self.disk,
            "serial" => self.serial = !self.serial,
            "audio" => {
                self.audio = !self.audio;
                if self.audio {
                    self.mode = Mode::Normal;
                }
            }
            _ => {}
        }
        self
    }
    pub fn enabled(self, device: &str) -> bool {
        match device {
            "network" => self.network,
            "disk" => self.disk,
            "serial" => self.serial,
            "audio" => self.audio,
            _ => false,
        }
    }
    pub fn from_parameters(v: &Value) -> Result<Self, Error> {
        let mode = Mode::parse(v["mode"].as_str().ok_or("missing mode")?)?;
        let flag = |name: &str| {
            v[name]
                .as_bool()
                .ok_or_else(|| -> Error { format!("missing {name}").into() })
        };
        let profile = v["pointerProfile"].as_str().unwrap_or("");
        if !["", "default", "windows"].contains(&profile)
            || v["revision"].as_str().is_none_or(str::is_empty)
        {
            return Err("invalid composition".into());
        }
        Ok(Self {
            mode,
            keyboard: flag("keyboard")?,
            relative: flag("relative")?,
            absolute: flag("absolute")?,
            network: flag("network")?,
            disk: flag("disk")?,
            serial: flag("serial")?,
            audio: flag("audio")?,
            windows_pointer: profile == "windows",
        })
    }
}
pub const FLAGS: [&str; 10] = [
    "usb.rndis0",
    "usb.ncm",
    "usb.disk0",
    "usb.acm",
    "usb.audio",
    "disable_hid",
    "usb.disable_keyboard",
    "usb.disable_relative",
    "usb.disable_absolute",
    "usb.pointer_windows",
];
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Snapshot {
    pub exists: bool,
    pub mode: u32,
    pub data: Vec<u8>,
}
pub fn snapshot(path: &Path) -> Result<Snapshot, Error> {
    let info = match fs::symlink_metadata(path) {
        Ok(info) => info,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Snapshot::default()),
        Err(e) => return Err(e.into()),
    };
    if !info.is_file() {
        return Err(format!(
            "USB configuration is not a regular file: {}",
            path.display()
        )
        .into());
    }
    Ok(Snapshot {
        exists: true,
        mode: info.permissions().mode() & 0o777,
        data: fs::read(path)?,
    })
}
pub fn restore(path: &Path, snapshot: &Snapshot) -> Result<(), Error> {
    if snapshot.exists {
        atomic_write(path, &snapshot.data, snapshot.mode)
    } else {
        match fs::remove_file(path) {
            Ok(()) => Ok(()),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(e) => Err(e.into()),
        }
    }
}
pub struct Transaction<'a> {
    pub root: &'a Path,
    pub install: &'a dyn Fn(Mode) -> Result<(), Error>,
    pub run: &'a dyn Fn(Action) -> Result<(), Error>,
    pub verify: &'a dyn Fn(Composition) -> Result<(), Error>,
}
impl Transaction<'_> {
    pub fn apply(&self, current: Composition, candidate: Composition) -> Result<(), Error> {
        candidate.validate()?;
        if current == candidate {
            return Ok(());
        }
        let boot = fsroot::resolve(self.root, Path::new("/boot"), false)?;
        let link = self.root.join("etc/init.d/S03usbdev");
        let missing = fs::symlink_metadata(&link).map_or_else(
            |e| e.kind() == std::io::ErrorKind::NotFound,
            |info| !info.file_type().is_symlink(),
        );
        let script = fsroot::resolve(self.root, Path::new("/etc/init.d/S03usbdev"), missing)?;
        let paths: Vec<PathBuf> = FLAGS
            .iter()
            .map(|name| boot.join(name))
            .chain(std::iter::once(script))
            .collect();
        let before: Vec<Snapshot> = paths
            .iter()
            .map(|path| snapshot(path))
            .collect::<Result<_, _>>()?;
        let mut stopped = false;
        let operation = (|| -> Result<(), Error> {
            (self.install)(candidate.mode)?;
            stopped = true;
            (self.run)(Action::UsbStop)?;
            let enabled = [
                false,
                candidate.network,
                candidate.disk,
                candidate.serial,
                candidate.audio,
                !(candidate.keyboard || candidate.relative || candidate.absolute),
                !candidate.keyboard,
                !candidate.relative,
                !candidate.absolute,
                candidate.windows_pointer,
            ];
            for (i, exists) in enabled.into_iter().enumerate() {
                // Existing mounted ISO paths and permissions survive unchanged.
                if exists != before[i].exists {
                    restore(
                        &paths[i],
                        &Snapshot {
                            exists,
                            mode: 0o644,
                            data: vec![],
                        },
                    )?;
                }
            }
            (self.run)(Action::UsbStart)?;
            (self.verify)(candidate)
        })();
        let Err(cause) = operation else {
            return Ok(());
        };
        let mut errors = Vec::new();
        let mut record = |result: Result<(), Error>| {
            if let Err(error) = result {
                errors.push(error.to_string());
            }
        };
        if stopped {
            record((self.run)(Action::UsbStop));
        }
        for (path, saved) in paths.iter().zip(&before) {
            record(restore(path, saved));
        }
        if stopped {
            match (self.run)(Action::UsbStart) {
                Ok(()) => record((self.verify)(current)),
                Err(e) => record(Err(e)),
            }
        }
        if errors.is_empty() {
            Err(cause)
        } else {
            Err(format!(
                "{cause}; restoring USB configuration also failed: {}",
                errors.join("; ")
            )
            .into())
        }
    }
}
pub fn verify(root: &Path, state: Composition) -> Result<(), Error> {
    let gadget = fsroot::resolve(root, Path::new("/sys/kernel/config/usb_gadget/g0"), false)?;
    let bound = !fs::read_to_string(gadget.join("UDC"))?.trim().is_empty();
    if bound == state.empty() {
        return Err("USB controller binding does not match the composition".into());
    }
    let linked = |name: &str| {
        fs::symlink_metadata(gadget.join("configs/c.1").join(name))
            .is_ok_and(|info| info.file_type().is_symlink())
    };
    if linked("hid.GS0") != state.keyboard
        || linked("hid.GS1") != state.relative
        || linked("hid.GS2") != state.absolute
        || linked("mass_storage.disk0") != state.disk
        || linked("acm.GS0") != state.serial
        || linked("uac1.audio0") != state.audio
        || (linked("rndis.usb0") || linked("ncm.usb0")) != state.network
    {
        return Err("USB functions do not match the requested composition".into());
    }
    if state.windows_pointer
        && !fs::read(gadget.join("functions/hid.GS2/report_desc"))
            .is_ok_and(|data| data.len() >= 2 && data[1] == 0x0d)
    {
        return Err("Windows pointer descriptor was not applied".into());
    }
    Ok(())
}
pub fn retire_getty(root: &Path, commands: &dyn crate::systemops::Executor) -> Result<(), Error> {
    let path = fsroot::resolve(root, Path::new("/etc/inittab"), false)?;
    let data = fs::read(&path)?;
    let mut updated = Vec::new();
    for line in data.split_inclusive(|b| *b == b'\n') {
        let text = String::from_utf8_lossy(line);
        if text.trim().starts_with("acm::respawn:") && text.contains("ttyGS0") {
            continue;
        }
        updated.extend_from_slice(line);
    }
    if data == updated {
        return Ok(());
    }
    let mode = fs::metadata(&path)?.permissions().mode() & 0o777;
    atomic_write(&path, &updated, mode)?;
    // Permanent migration: a composition rollback must not revive this getty.
    commands.run(Action::ReloadInit, Duration::from_secs(10))
}
