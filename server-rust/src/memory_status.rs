//! Read-only Linux memory/swap/video telemetry. No swap or boot mutations.
use crate::{
    api::{error, ok},
    fsroot, Error, Runtime,
};
use axum::response::Response;
use serde::Serialize;
use std::{collections::BTreeMap, fs, path::Path};
fn zero(value: &u64) -> bool {
    *value == 0
}
#[derive(Clone, Debug, Default, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Swap {
    pub enabled: bool,
    pub available: bool,
    #[serde(rename = "sizeMiB")]
    pub size_mib: i64,
    pub used_bytes: u64,
    pub priority: i64,
    #[serde(skip_serializing_if = "zero")]
    pub memory_bytes: u64,
    #[serde(skip_serializing_if = "zero")]
    pub compressed_bytes: u64,
    #[serde(skip_serializing_if = "zero")]
    pub original_bytes: u64,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub algorithm: String,
    pub recompress: bool,
    pub recompress_available: bool,
    pub recompress_ready: bool,
}
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Video {
    pub active: String,
    pub selected: String,
    #[serde(rename = "sizeMiB")]
    pub size_mib: i64,
    pub available: bool,
    pub reboot_required: bool,
}
#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub video_memory: Video,
    pub total_bytes: u64,
    pub available_bytes: u64,
    pub used_bytes: u64,
    pub cached_bytes: u64,
    pub swap_total_bytes: u64,
    pub swap_used_bytes: u64,
    pub video_bytes: u64,
    pub zram: Swap,
    pub sd: Swap,
}
fn unsigned(text: &str) -> Option<u64> {
    (!text.is_empty() && text.bytes().all(|b| b.is_ascii_digit()))
        .then(|| text.parse().ok())
        .flatten()
}
fn scan(text: &str) -> impl Iterator<Item = &str> {
    text.split_inclusive('\n')
        .take_while(|line| line.strip_suffix('\n').unwrap_or(line).len() < 65536)
        .map(|line| {
            line.strip_suffix('\n')
                .unwrap_or(line)
                .strip_suffix('\r')
                .unwrap_or(line.strip_suffix('\n').unwrap_or(line))
        })
}
pub fn counters(text: &str) -> BTreeMap<String, u64> {
    let mut values = BTreeMap::new();
    for line in scan(text) {
        let fields: Vec<_> = line.split_whitespace().collect();
        if fields.len() < 2 {
            continue;
        }
        if let Some(value) = unsigned(fields[1]) {
            values.insert(
                fields[0].strip_suffix(':').unwrap_or(fields[0]).into(),
                value.wrapping_mul(1024),
            );
        }
    }
    values
}
pub fn active_swaps(text: &str) -> BTreeMap<String, Swap> {
    let mut swaps = BTreeMap::new();
    for line in scan(text) {
        let fields: Vec<_> = line.split_whitespace().collect();
        if fields.len() < 5 || fields[0] == "Filename" {
            continue;
        }
        let (Ok(size), Some(used), Ok(priority)) = (
            fields[2].parse::<i64>(),
            unsigned(fields[3]),
            fields[4].parse::<i64>(),
        ) else {
            continue;
        };
        if size <= 0 {
            continue;
        }
        swaps.insert(
            fields[0].into(),
            Swap {
                enabled: true,
                available: true,
                size_mib: size.wrapping_add(1023) / 1024,
                used_bytes: used.wrapping_mul(1024),
                priority,
                ..Default::default()
            },
        );
    }
    swaps
}
pub fn recompress_enabled(text: &str) -> bool {
    let mut enabled = false;
    for line in text.split('\n') {
        match line {
            "ZRAM_RECOMPRESS=1" => enabled = true,
            "ZRAM_RECOMPRESS=0" => enabled = false,
            _ => {}
        }
    }
    enabled
}
pub fn zstd_ready(text: &str) -> bool {
    text.split('\n')
        .any(|line| line.starts_with("#1:") && line.contains("[zstd]"))
}
fn read(root: &Path, path: &str) -> Result<Vec<u8>, Error> {
    Ok(fs::read(fsroot::resolve(root, Path::new(path), false)?)?)
}
fn text(root: &Path, path: &str) -> String {
    read(root, path)
        .map(|bytes| crate::json_text::text(&bytes).into_owned())
        .unwrap_or_default()
}
fn exists(root: &Path, path: &str) -> bool {
    fsroot::resolve(root, Path::new(path), false)
        .and_then(|path| fs::metadata(path).map_err(Into::into))
        .is_ok()
}
fn mode(mode: &str) -> bool {
    matches!(mode, "cma" | "fixed")
}
pub fn video(root: &Path) -> Video {
    let fdt = "/sys/firmware/devicetree/base";
    let read = |path: &str| {
        text(root, path)
            .trim_matches(['\0', ' ', '\r', '\n'])
            .to_owned()
    };
    let mut active = read(&format!("{fdt}/nanokvm,video-memory-mode"));
    if !mode(&active) {
        active = if exists(
            root,
            &format!("{fdt}/cvitek-ion/heap-carveout/nanokvm,cma-backend"),
        ) {
            "cma"
        } else if read(&format!("{fdt}/reserved-memory/ion/compatible")) == "ion-region" {
            "fixed"
        } else {
            "unknown"
        }
        .into();
    }
    let mut selected = read("/etc/kvm/video-memory-mode");
    if !mode(&selected) {
        selected = "cma".into();
    }
    let board = read(&format!("{fdt}/sipeed,board-revision"));
    let available = matches!(board.as_str(), "alpha" | "beta" | "pcie" | "lite")
        && exists(root, &format!("/usr/lib/nanokvm/boot/{board}.sd"))
        && exists(root, &format!("/usr/lib/nanokvm/boot/{board}-fixed.sd"));
    Video {
        reboot_required: active != "unknown" && active != selected,
        active,
        selected,
        size_mib: 64,
        available,
    }
}
pub fn read_status(root: &Path) -> Result<Status, Error> {
    let raw = read(root, "/proc/meminfo")?;
    let values = counters(&crate::json_text::text(&raw));
    let get = |name: &str| values.get(name).copied().unwrap_or(0);
    let total = get("MemTotal");
    if total == 0 {
        return Err("memory statistics unavailable".into());
    }
    let available = get("MemAvailable").min(total);
    let cached = get("Cached")
        .wrapping_add(get("Buffers"))
        .wrapping_add(get("SReclaimable"));
    let swap_total = get("SwapTotal");
    let raw = read(root, "/proc/swaps")?;
    let mut active = active_swaps(&crate::json_text::text(&raw));
    let mut zram = active.remove("/dev/zram0").unwrap_or_default();
    let mut sd = active.remove("/swapfile").unwrap_or_default();
    if !zram.enabled {
        zram.size_mib = 64;
    }
    if !sd.enabled {
        sd.size_mib = 256;
    }
    let config = text(root, "/etc/kvm/memory.conf");
    zram.recompress = recompress_enabled(&config);
    zram.recompress_available =
        exists(root, "/sys/block/zram0/recompress") && exists(root, "/sys/block/zram0/idle");
    zram.recompress_ready =
        zram.enabled && zstd_ready(&text(root, "/sys/block/zram0/recomp_algorithm"));
    for line in config.split('\n') {
        let Some((key, value)) = line.split_once('=') else {
            continue;
        };
        let Ok(size) = value.parse::<i64>() else {
            continue;
        };
        match key {
            "ZRAM_SIZE" if !zram.enabled && [32, 64, 128, 162].contains(&size) => {
                zram.size_mib = size
            }
            "SD_SIZE" if !sd.enabled && [128, 256, 512].contains(&size) => sd.size_mib = size,
            _ => {}
        }
    }
    let helper = exists(root, "/etc/init.d/S38memory");
    sd.available = helper;
    let release = text(root, "/proc/sys/kernel/osrelease");
    let module_dir = format!("/lib/modules/{}/kernel/drivers/block/zram", release.trim());
    let modules = fsroot::resolve(root, Path::new(&module_dir), false)
        .ok()
        .and_then(|path| fs::read_dir(path).ok())
        .is_some_and(|mut entries| {
            entries.any(|entry| {
                entry.ok().is_some_and(|entry| {
                    entry.file_name().as_encoded_bytes().starts_with(b"zram.ko")
                })
            })
        });
    zram.available =
        helper && (exists(root, "/sys/block/zram0") || exists(root, "/sys/module/zram") || modules);
    zram.algorithm = "lz4".into();
    let mm = text(root, "/sys/block/zram0/mm_stat");
    let fields: Vec<_> = mm.split_whitespace().collect();
    if fields.len() >= 3 {
        zram.original_bytes = unsigned(fields[0]).unwrap_or(0);
        zram.compressed_bytes = unsigned(fields[1]).unwrap_or(0);
        zram.memory_bytes = unsigned(fields[2]).unwrap_or(0);
    }
    for field in text(root, "/sys/block/zram0/comp_algorithm").split_whitespace() {
        if field.starts_with('[') {
            zram.algorithm = field.trim_matches(['[', ']']).into();
        }
    }
    let video_bytes =
        unsigned(text(root, "/sys/kernel/debug/ion/carveout/num_of_alloc_bytes").trim())
            .unwrap_or(0);
    Ok(Status {
        video_memory: video(root),
        total_bytes: total,
        available_bytes: available,
        used_bytes: total - available,
        cached_bytes: cached.saturating_sub(get("Shmem")),
        swap_total_bytes: swap_total,
        swap_used_bytes: swap_total.saturating_sub(get("SwapFree")),
        video_bytes,
        zram,
        sd,
    })
}
pub(crate) fn get(runtime: &Runtime) -> Response {
    match read_status(&runtime.root) {
        Ok(status) => ok(serde_json::to_value(status).unwrap()),
        Err(_) => error(-1, "Failed to read memory statistics"),
    }
}
