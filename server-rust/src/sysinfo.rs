//! Firmware identity and read-only interface discovery, with fixture injection.
use crate::{api::ok, fsroot, Error, Runtime};
use axum::response::Response;
use serde::Serialize;
use std::{
    collections::BTreeMap,
    ffi::CStr,
    fs,
    net::{IpAddr, Ipv4Addr, Ipv6Addr},
    path::{Path, PathBuf},
    sync::Arc,
};
#[derive(Clone, Debug)]
pub struct Interface {
    pub index: u32,
    pub name: String,
    pub up: bool,
    pub running: bool,
    pub addresses: Vec<IpAddr>,
}
#[derive(Clone, Debug)]
pub struct TelemetryInterface {
    pub interface: Interface,
    pub loopback: bool,
    pub kind: String,
    pub mac: String,
    pub mtu: i64,
    pub addresses: Vec<String>,
}
pub trait Interfaces: Send + Sync {
    fn list(&self) -> Result<Vec<Interface>, Error>;
    fn telemetry(&self) -> Result<Vec<TelemetryInterface>, Error> {
        Err("network telemetry backend unavailable".into())
    }
}
pub struct Native {
    root: PathBuf,
}
impl Native {
    pub fn new(root: PathBuf) -> Self {
        Self { root }
    }
}
impl Interfaces for Native {
    fn list(&self) -> Result<Vec<Interface>, Error> {
        Ok(self
            .collect(false)?
            .into_iter()
            .map(|iface| iface.interface)
            .collect())
    }
    fn telemetry(&self) -> Result<Vec<TelemetryInterface>, Error> {
        self.collect(true)
    }
}
impl Native {
    fn collect(&self, detailed: bool) -> Result<Vec<TelemetryInterface>, Error> {
        if self.root != Path::new("/") {
            return Err("interface discovery unavailable in isolated root".into());
        }
        let links = if detailed {
            crate::link_telemetry::native()?
        } else {
            BTreeMap::new()
        };
        let mut first = std::ptr::null_mut();
        if unsafe { libc::getifaddrs(&mut first) } != 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        struct Guard(*mut libc::ifaddrs);
        impl Drop for Guard {
            fn drop(&mut self) {
                unsafe {
                    libc::freeifaddrs(self.0);
                }
            }
        }
        let _guard = Guard(first);
        let mut interfaces = BTreeMap::<u32, TelemetryInterface>::new();
        let mut cursor = first;
        while !cursor.is_null() {
            // getifaddrs owns a linked list valid until Guard drops.
            let item = unsafe { &*cursor };
            cursor = item.ifa_next;
            if item.ifa_name.is_null() {
                continue;
            }
            let index = unsafe { libc::if_nametoindex(item.ifa_name) };
            if index == 0 {
                continue;
            }
            let interface = interfaces.entry(index).or_insert_with(|| {
                let link = links.get(&index).cloned().unwrap_or_default();
                TelemetryInterface {
                    interface: Interface {
                        index,
                        name: crate::json_text::text(
                            unsafe { CStr::from_ptr(item.ifa_name) }.to_bytes(),
                        )
                        .into_owned(),
                        up: item.ifa_flags & libc::IFF_UP as u32 != 0,
                        running: item.ifa_flags & libc::IFF_RUNNING as u32 != 0,
                        addresses: Vec::new(),
                    },
                    loopback: item.ifa_flags & libc::IFF_LOOPBACK as u32 != 0,
                    kind: link.kind,
                    mac: link.mac,
                    mtu: link.mtu,
                    addresses: Vec::new(),
                }
            });
            if item.ifa_addr.is_null() {
                continue;
            }
            let family = unsafe { (*item.ifa_addr).sa_family } as i32;
            let ip = match family {
                libc::AF_INET => {
                    let address = unsafe { &*item.ifa_addr.cast::<libc::sockaddr_in>() };
                    IpAddr::V4(Ipv4Addr::from(address.sin_addr.s_addr.to_ne_bytes()))
                }
                libc::AF_INET6 => {
                    let address = unsafe { &*item.ifa_addr.cast::<libc::sockaddr_in6>() };
                    IpAddr::V6(Ipv6Addr::from(address.sin6_addr.s6_addr))
                }
                _ => continue,
            };
            interface.interface.addresses.push(ip);
            if !item.ifa_netmask.is_null()
                && unsafe { (*item.ifa_netmask).sa_family } as i32 == family
            {
                let mask = match family {
                    libc::AF_INET => IpAddr::V4(Ipv4Addr::from(
                        unsafe { &*item.ifa_netmask.cast::<libc::sockaddr_in>() }
                            .sin_addr
                            .s_addr
                            .to_ne_bytes(),
                    )),
                    libc::AF_INET6 => IpAddr::V6(Ipv6Addr::from(
                        unsafe { &*item.ifa_netmask.cast::<libc::sockaddr_in6>() }
                            .sin6_addr
                            .s6_addr,
                    )),
                    _ => continue,
                };
                if let Some(address) = cidr(ip, mask) {
                    interface.addresses.push(address)
                }
            }
        }
        Ok(interfaces.into_values().collect())
    }
}
#[derive(Debug, Serialize)]
pub struct IP {
    pub name: String,
    pub addr: String,
    pub version: &'static str,
    #[serde(rename = "type")]
    pub kind: &'static str,
}
pub fn select_ipv4(interfaces: Vec<Interface>) -> Option<Vec<IP>> {
    let mut ips = Vec::new();
    for interface in interfaces {
        if !interface.up || !interface.running {
            continue;
        }
        let kind = if interface.name.starts_with("eth") || interface.name.starts_with("en") {
            "Wired"
        } else if interface.name.starts_with("wlan") || interface.name.starts_with("wl") {
            "Wireless"
        } else {
            continue;
        };
        // Baseline selects the first interface address before its IPv4 filter.
        let Some(first) = interface.addresses.first() else {
            continue;
        };
        let ipv4 = match first {
            IpAddr::V4(ip) => Some(*ip),
            IpAddr::V6(ip) => ip.to_ipv4_mapped(),
        };
        if let Some(ip) = ipv4 {
            ips.push(IP {
                name: interface.name,
                addr: ip.to_string(),
                version: "IPv4",
                kind,
            });
        }
    }
    (!ips.is_empty()).then_some(ips)
}
#[derive(Debug, Serialize)]
pub struct Info {
    pub ips: Option<Vec<IP>>,
    pub mdns: String,
    pub image: String,
    pub application: String,
    #[serde(rename = "deviceKey")]
    pub device_key: String,
}
pub struct Manager {
    root: PathBuf,
    interfaces: Arc<dyn Interfaces>,
}
impl Manager {
    pub fn new(root: PathBuf, interfaces: Arc<dyn Interfaces>) -> Self {
        Self { root, interfaces }
    }
    fn text(&self, path: &str) -> Result<String, Error> {
        let path = fsroot::resolve(&self.root, Path::new(path), false)?;
        Ok(crate::json_text::text(&fs::read(path)?).replace('\n', ""))
    }
    pub fn mdns_enabled(&self) -> bool {
        self.text("/run/avahi-daemon/pid")
            .is_ok_and(|pid| !pid.is_empty())
    }
    pub fn image(&self) -> String {
        let image = self.text("/boot/ver").unwrap_or_default();
        match image.as_str() {
            "2024-06-23-20-59-2d2bfb.img" => "v1.0.0".into(),
            "2024-07-23-20-18-587710.img" => "v1.1.0".into(),
            "2024-08-08-19-44-bef2ca.img" => "v1.2.0".into(),
            "2024-11-13-09-59-9c961a.img" => "v1.3.0".into(),
            "2025-02-17-19-08-3649fe.img" => "v1.4.0".into(),
            "2025-04-17-14-21-98d17d.img" => "v1.4.1".into(),
            "2026-01-05-1_4_1.img" => "v1.4.2".into(),
            _ => image,
        }
    }
    pub fn application(&self) -> String {
        self.text("/kvmapp/version")
            .unwrap_or_else(|_| "1.0.0".into())
    }
    pub fn read(&self) -> Info {
        Info {
            ips: self.interfaces.list().ok().and_then(select_ipv4),
            mdns: if self.mdns_enabled() {
                self.text("/etc/hostname")
                    .map(|name| format!("{name}.local"))
                    .unwrap_or_default()
            } else {
                String::new()
            },
            image: self.image(),
            application: self.application(),
            device_key: self.text("/device_key").unwrap_or_default(),
        }
    }
}
pub(crate) fn get(runtime: &Runtime) -> Response {
    ok(serde_json::to_value(runtime.info.read()).unwrap())
}
pub(crate) fn mdns(runtime: &Runtime) -> Response {
    ok(serde_json::json!({"enabled":runtime.info.mdns_enabled()}))
}

pub(crate) fn title(runtime: &Runtime) -> Response {
    match runtime.info.text("/etc/kvm/web-title") {
        Ok(title) => ok(serde_json::json!({"title":title})),
        Err(_) => crate::api::error(-1, "read web title failed"),
    }
}

/// Go IPNet.String preserves the address, with mapped IPv4 and non-CIDR masks.
pub fn cidr(address: IpAddr, mask: IpAddr) -> Option<String> {
    let (address, mask) = match (address, mask) {
        (IpAddr::V4(address), IpAddr::V4(mask)) => (address.to_string(), mask.octets().to_vec()),
        (IpAddr::V6(address), IpAddr::V6(mask)) => {
            if let Some(mapped) = address.to_ipv4_mapped() {
                (mapped.to_string(), mask.octets()[12..].to_vec())
            } else {
                (address.to_string(), mask.octets().to_vec())
            }
        }
        _ => return None,
    };
    let mut prefix = 0;
    let mut zeros = false;
    let mut contiguous = true;
    for byte in &mask {
        for bit in (0..8).rev() {
            if byte & (1 << bit) != 0 {
                if zeros {
                    contiguous = false
                }
                prefix += 1
            } else {
                zeros = true
            }
        }
    }
    Some(format!(
        "{address}/{}",
        if contiguous {
            prefix.to_string()
        } else {
            mask.iter().map(|b| format!("{b:02x}")).collect::<String>()
        }
    ))
}
