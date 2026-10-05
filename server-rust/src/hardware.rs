//! Baseline image and board detection; unknown Enhanced boards expose no pins.
use std::{fs, path::Path};
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Hardware {
    pub version: &'static str,
    pub power: String,
    pub reset: String,
    pub power_led: String,
    pub hdd_led: String,
}
impl Hardware {
    pub fn detect(root: &Path) -> Self {
        let read = |path: &str| {
            crate::fsroot::resolve(root, Path::new(path), false)
                .ok()
                .and_then(|path| fs::read(path).ok())
                .unwrap_or_default()
        };
        let raw = read("/etc/kvm/hw");
        let legacy = String::from_utf8_lossy(&raw).replace('\n', "");
        let version = match legacy.as_str() {
            "beta" => "Beta",
            "pcie" => "PCIE",
            _ => "Alpha",
        };
        let mut result = Self {
            version,
            power: "/sys/class/gpio/gpio503/value".into(),
            reset: format!(
                "/sys/class/gpio/gpio{}/value",
                if version == "Alpha" { 507 } else { 505 }
            ),
            power_led: "/sys/class/gpio/gpio504/value".into(),
            hdd_led: if version == "Alpha" {
                "/sys/class/gpio/gpio505/value".into()
            } else {
                String::new()
            },
        };
        if !String::from_utf8_lossy(&read("/etc/nanokvm-buildroot")).contains("flavour=enhanced") {
            return result;
        }
        result.power.clear();
        result.reset.clear();
        result.power_led.clear();
        result.hdd_led.clear();
        let declared = crate::fsroot::resolve(root, Path::new("/etc/kvm/board-profile"), false)
            .ok()
            .and_then(|path| fs::read(path).ok())
            .unwrap_or(raw);
        match String::from_utf8_lossy(&declared).trim() {
            "lite" => {
                result.version = "Beta";
                return result;
            }
            "alpha" => {
                result.version = "Alpha";
                result.reset = "gpio-v2:3020000.gpio:27".into();
                result.hdd_led = "gpio-v2:3020000.gpio:25".into();
            }
            "beta" => {
                result.version = "Beta";
                result.reset = "gpio-v2:3020000.gpio:25".into();
            }
            "pcie" => {
                result.version = "PCIE";
                result.reset = "gpio-v2:3020000.gpio:25".into();
                result.hdd_led = "gpio-v2:5021000.gpio:3".into();
            }
            _ => return result,
        }
        result.power = "gpio-v2:3020000.gpio:23".into();
        result.power_led = "gpio-v2:3020000.gpio:24".into();
        result
    }
}
