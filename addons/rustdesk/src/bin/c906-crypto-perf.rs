#![allow(dead_code)]
// SPDX-License-Identifier: AGPL-3.0-only
#[path = "../frame_crypto.rs"]
mod frame_crypto;
#[path = "../framing.rs"]
mod framing;
#[path = "../protocol.rs"]
mod protocol;
use crypto_secretbox::{
    aead::{Aead, AeadInPlace, KeyInit},
    Key, Nonce, Tag, XSalsa20Poly1305,
};
use std::{hint::black_box, time::Instant};
#[cfg(target_arch = "riscv64")]
fn controls() -> [u64; 3] {
    let (a, b, c);
    unsafe {
        std::arch::asm!("csrr {a},0x003","csrr {b},0x00a","csrr {c},0x009",a=out(reg)a,b=out(reg)b,c=out(reg)c,options(nostack));
    }
    [a, b, c]
}
#[cfg(not(target_arch = "riscv64"))]
fn controls() -> [u64; 3] {
    [0; 3]
}
#[cfg(target_arch = "riscv64")]
fn set_controls(vxrm: u64, vxsat: u64) {
    unsafe {
        std::arch::asm!("csrw 0x00a,{a}","csrw 0x009,{b}",a=in(reg)vxrm,b=in(reg)vxsat,options(nostack));
    }
}
#[cfg(not(target_arch = "riscv64"))]
fn set_controls(_: u64, _: u64) {}
struct RestoreControls([u64; 3]);
impl Drop for RestoreControls {
    fn drop(&mut self) {
        set_controls(self.0[1], self.0[2]);
    }
}
fn qualify_active_controls() -> usize {
    let _restore = RestoreControls(controls());
    let key = Key::from([0x53; 32]);
    let nonce = Nonce::from([0x91; 24]);
    let old = XSalsa20Poly1305::new(&key);
    let new = frame_crypto::FrameCipher::new(&key);
    let mut cases = 0;
    for vxrm in 0..4 {
        for vxsat in 0..2 {
            for n in [255, 256, 511, 512, 513, 1420, 4096, 65536] {
                for offset in 0..4 {
                    let plain = pattern(n, 3);
                    let reference = old.encrypt(&nonce, plain.as_slice()).unwrap();
                    let mut guarded = vec![0xa7; n + offset + 64];
                    let start = 32 + offset;
                    guarded[start..start + n].copy_from_slice(&plain);
                    // Prepare heap buffers before capturing the function contract.
                    set_controls(vxrm, vxsat);
                    let before = controls();
                    let tag = new.encrypt(&nonce, &mut guarded[start..start + n]).unwrap();
                    assert_eq!(
                        controls(),
                        before,
                        "active encrypt controls n={n} offset={offset} vxrm={vxrm} vxsat={vxsat}"
                    );
                    assert_eq!(tag.as_slice(), &reference[..16]);
                    assert_eq!(&guarded[start..start + n], &reference[16..]);
                    new.decrypt(&nonce, &mut guarded[start..start + n], &tag)
                        .unwrap();
                    assert_eq!(
                        controls(),
                        before,
                        "active decrypt controls n={n} offset={offset} vxrm={vxrm} vxsat={vxsat}"
                    );
                    assert_eq!(&guarded[start..start + n], plain.as_slice());
                    assert!(guarded[..start]
                        .iter()
                        .chain(guarded[start + n..].iter())
                        .all(|&b| b == 0xa7));
                    cases += 1;
                }
            }
        }
    }
    cases
}
fn pattern(n: usize, p: usize) -> Vec<u8> {
    (0..n)
        .map(|i| match p {
            0 => 0,
            1 => 255,
            2 => (i * 37 + 11) as u8,
            _ => ((i * 1664525 + 1013904223) ^ (i >> 3)) as u8,
        })
        .collect()
}
fn qualify() {
    let sizes = [
        0, 1, 15, 16, 31, 32, 63, 64, 65, 127, 128, 129, 255, 256, 257, 511, 512, 513, 1023, 1024,
        1025, 1420, 1500, 2048, 4095, 4096, 4097, 8191, 8192, 8193, 16384, 65535, 65536, 65537,
        262144,
    ];
    let mut cases = 0;
    for keybyte in [0, 0x53, 255] {
        let key = Key::from([keybyte; 32]);
        let old = XSalsa20Poly1305::new(&key);
        let new = frame_crypto::FrameCipher::new(&key);
        for n in sizes {
            for offset in 0..8 {
                for p in 0..4 {
                    let mut nonce = Nonce::default();
                    nonce[..8].copy_from_slice(&(cases as u64 + 1).to_le_bytes());
                    nonce[8..].fill(keybyte.wrapping_add(p as u8));
                    let plain = pattern(n, p);
                    let reference = old.encrypt(&nonce, plain.as_slice()).unwrap();
                    let mut buf = vec![0xa7; n + offset + 64];
                    let start = 32 + offset;
                    buf[start..start + n].copy_from_slice(&plain);
                    let before = controls();
                    let tag = new.encrypt(&nonce, &mut buf[start..start + n]).unwrap();
                    assert_eq!(controls(), before, "encrypt controls");
                    assert_eq!(tag.as_slice(), &reference[..16]);
                    assert_eq!(&buf[start..start + n], &reference[16..]);
                    assert!(buf[..start]
                        .iter()
                        .chain(buf[start + n..].iter())
                        .all(|&b| b == 0xa7));
                    let mut bad = tag;
                    bad[0] ^= 1;
                    assert!(new
                        .decrypt(&nonce, &mut buf[start..start + n], &bad)
                        .is_err());
                    assert_eq!(
                        &buf[start..start + n],
                        &reference[16..],
                        "bad tag changed output"
                    );
                    new.decrypt(&nonce, &mut buf[start..start + n], &tag)
                        .unwrap();
                    assert_eq!(controls(), before, "decrypt controls");
                    assert_eq!(&buf[start..start + n], plain.as_slice());
                    assert!(buf[..start]
                        .iter()
                        .chain(buf[start + n..].iter())
                        .all(|&b| b == 0xa7));
                    cases += 1;
                }
            }
        }
    }
    let csr_cases = qualify_active_controls();
    println!("PASS crypto_cases={cases} active_csr_cases={csr_cases} scalar_reference tag_prefix tails offsets guards encrypt_decrypt modified_tags FCSR_VXRM_VXSAT");
}
fn cpu_time_ns() -> u64 {
    let mut time = libc::timespec {
        tv_sec: 0,
        tv_nsec: 0,
    };
    assert_eq!(
        unsafe { libc::clock_gettime(libc::CLOCK_PROCESS_CPUTIME_ID, &mut time) },
        0
    );
    time.tv_sec as u64 * 1_000_000_000 + time.tv_nsec as u64
}
fn bench() {
    let key = Key::from([0x53; 32]);
    let nonce = Nonce::from([0x91; 24]);
    let old = XSalsa20Poly1305::new(&key);
    let new = frame_crypto::FrameCipher::new(&key);
    println!("kind,mode,bytes,repetition,iterations,elapsed_ns,cpu_ns");
    for rep in 0..7 {
        for n in [64, 256, 512, 768, 1420, 1500, 4096, 8192, 16384, 65536] {
            let plain = pattern(n, 3);
            let reference = old.encrypt(&nonce, plain.as_slice()).unwrap();
            let iters = (1048576usize / n).clamp(16, 4096);
            let mut buf = vec![0; n];
            let tag = Tag::clone_from_slice(&reference[..16]);
            for pos in 0..3 {
                let mode = if rep % 2 == 0 { pos } else { 2 - pos };
                let cpu_before = cpu_time_ns();
                let start = Instant::now();
                for _ in 0..iters {
                    match mode {
                        0 => {
                            black_box(old.encrypt(&nonce, black_box(plain.as_slice())).unwrap());
                        }
                        1 => {
                            buf.copy_from_slice(&plain);
                            black_box(
                                old.encrypt_in_place_detached(&nonce, b"", &mut buf)
                                    .unwrap(),
                            );
                        }
                        _ => {
                            buf.copy_from_slice(&plain);
                            black_box(new.encrypt(&nonce, &mut buf).unwrap());
                        }
                    }
                }
                let elapsed = start.elapsed().as_nanos();
                let cpu = cpu_time_ns() - cpu_before;
                println!("encrypt,{mode},{n},{rep},{iters},{elapsed},{cpu}");
                let cpu_before = cpu_time_ns();
                let start = Instant::now();
                for _ in 0..iters {
                    match mode {
                        0 => {
                            black_box(
                                old.decrypt(&nonce, black_box(reference.as_slice()))
                                    .unwrap(),
                            );
                        }
                        1 => {
                            buf.copy_from_slice(&reference[16..]);
                            old.decrypt_in_place_detached(&nonce, b"", &mut buf, &tag)
                                .unwrap();
                            black_box(&buf);
                        }
                        _ => {
                            buf.copy_from_slice(&reference[16..]);
                            new.decrypt(&nonce, &mut buf, &tag).unwrap();
                            black_box(&buf);
                        }
                    }
                }
                let elapsed = start.elapsed().as_nanos();
                let cpu = cpu_time_ns() - cpu_before;
                println!("decrypt,{mode},{n},{rep},{iters},{elapsed},{cpu}");
            }
        }
    }
}
struct MeasuredSink;
impl tokio::io::AsyncWrite for MeasuredSink {
    fn poll_write(
        self: std::pin::Pin<&mut Self>,
        _: &mut std::task::Context<'_>,
        buf: &[u8],
    ) -> std::task::Poll<std::io::Result<usize>> {
        std::task::Poll::Ready(Ok(black_box(buf).len()))
    }
    fn poll_flush(
        self: std::pin::Pin<&mut Self>,
        _: &mut std::task::Context<'_>,
    ) -> std::task::Poll<std::io::Result<()>> {
        std::task::Poll::Ready(Ok(()))
    }
    fn poll_shutdown(
        self: std::pin::Pin<&mut Self>,
        _: &mut std::task::Context<'_>,
    ) -> std::task::Poll<std::io::Result<()>> {
        std::task::Poll::Ready(Ok(()))
    }
}
struct CountAllocator;
static ALLOCATIONS: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
unsafe impl std::alloc::GlobalAlloc for CountAllocator {
    unsafe fn alloc(&self, layout: std::alloc::Layout) -> *mut u8 {
        ALLOCATIONS.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        std::alloc::System.alloc(layout)
    }
    unsafe fn dealloc(&self, ptr: *mut u8, layout: std::alloc::Layout) {
        std::alloc::System.dealloc(ptr, layout);
    }
    unsafe fn realloc(&self, ptr: *mut u8, layout: std::alloc::Layout, size: usize) -> *mut u8 {
        ALLOCATIONS.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        std::alloc::System.realloc(ptr, layout, size)
    }
}
#[global_allocator]
static ALLOCATOR: CountAllocator = CountAllocator;
// A sink removes network scheduling from this comparison while retaining
// the actual protobuf -> encrypted length-framed writer path.
fn frame_bench() {
    use prost::Message as _;
    use tokio::io::AsyncWriteExt as _;
    let runtime = tokio::runtime::Builder::new_current_thread()
        .build()
        .unwrap();
    runtime.block_on(async {
        let key = Key::from([0x53; 32]);
        let old = XSalsa20Poly1305::new(&key);
        println!("kind,mode,bytes,repetition,iterations,elapsed_ns,allocations,cpu_ns");
        for rep in 0..7 {
            for n in [64, 256, 512, 1420, 4096, 16384, 65536, 262144] {
                let message = protocol::Message {
                    union: Some(protocol::message::Union::Hash(protocol::Hash {
                        salt: "x".repeat(n),
                        challenge: "framing benchmark".to_owned(),
                    })),
                };
                let iters = (1048576usize / n).clamp(8, 4096);
                for pos in 0..2 {
                    let mode = if rep % 2 == 0 { pos } else { 1 - pos };
                    let mut sink = MeasuredSink;
                    let mut writer = framing::FrameWriter::new(
                        MeasuredSink,
                        Some(framing::SessionKey::new([0x53; 32])),
                    );
                    if mode == 1 {
                        writer.write(&message).await.unwrap();
                    }
                    let alloc_before = ALLOCATIONS.load(std::sync::atomic::Ordering::Relaxed);
                    let cpu_before = cpu_time_ns();
                    let start = Instant::now();
                    for sequence in 1..=iters {
                        if mode == 0 {
                            let plain = message.encode_to_vec();
                            let mut nonce = Nonce::default();
                            nonce[..8].copy_from_slice(&(sequence as u64).to_le_bytes());
                            let payload = old.encrypt(&nonce, plain.as_slice()).unwrap();
                            let n = payload.len();
                            let (encoded, bytes) = if n <= 0x3f {
                                (n << 2, 1)
                            } else if n <= 0x3fff {
                                ((n << 2) | 1, 2)
                            } else if n <= 0x3fffff {
                                ((n << 2) | 2, 3)
                            } else {
                                ((n << 2) | 3, 4)
                            };
                            sink.write_all(&encoded.to_le_bytes()[..bytes])
                                .await
                                .unwrap();
                            sink.write_all(black_box(&payload)).await.unwrap();
                            sink.flush().await.unwrap();
                        } else {
                            writer.write(black_box(&message)).await.unwrap();
                        }
                    }
                    let elapsed = start.elapsed().as_nanos();
                    let allocations =
                        ALLOCATIONS.load(std::sync::atomic::Ordering::Relaxed) - alloc_before;
                    let cpu = cpu_time_ns() - cpu_before;
                    println!("frame_write,{mode},{n},{rep},{iters},{elapsed},{allocations},{cpu}");
                }
            }
        }
    });
}
fn main() {
    match std::env::args().nth(1).as_deref() {
        Some("qualify") => qualify(),
        Some("controls") => println!("PASS active_csr_cases={}", qualify_active_controls()),
        Some("bench") => bench(),
        Some("frame-bench") => frame_bench(),
        _ => panic!("qualify|bench|frame-bench"),
    }
}
