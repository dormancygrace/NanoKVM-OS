//! Read-only RTM_GETLINK dump with owned bytes and a bounded receive deadline.
use crate::Error;
use std::{
    collections::BTreeMap,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    time::{Duration, Instant},
};
#[derive(Clone, Debug, Default)]
pub(crate) struct Link {
    pub kind: String,
    pub mac: String,
    pub mtu: i64,
}
pub(crate) fn attribute(mut data: &[u8], wanted: u16) -> Option<&[u8]> {
    while data.len() >= 4 {
        let length = usize::from(u16::from_ne_bytes(data[..2].try_into().ok()?));
        let kind = u16::from_ne_bytes(data[2..4].try_into().ok()?) & 0x3fff;
        if length < 4 || length > data.len() {
            return None;
        }
        if kind == wanted {
            return Some(&data[4..length]);
        }
        data = data.get((length + 3) & !3..)?;
    }
    None
}
pub(crate) fn parse_links(data: &[u8], result: &mut BTreeMap<u32, Link>) -> Result<bool, Error> {
    let mut at = 0;
    let mut done = false;
    while at < data.len() {
        let header = data.get(at..at + 16).ok_or("short netlink header")?;
        let length = u32::from_ne_bytes(header[..4].try_into()?) as usize;
        if length < 16 {
            return Err("invalid netlink message length".into());
        }
        let message = data
            .get(at + 16..at + length)
            .ok_or("short netlink message")?;
        let kind = u16::from_ne_bytes(header[4..6].try_into()?);
        if u32::from_ne_bytes(header[8..12].try_into()?) != 1 {
            return Err("netlink sequence mismatch".into());
        }
        let flags = u16::from_ne_bytes(header[6..8].try_into()?);
        if flags & 0x10 != 0 {
            return Err("interrupted netlink dump".into());
        }
        match kind {
            2 => return Err("netlink request failed".into()),
            3 => {
                if message.len() >= 4 && i32::from_ne_bytes(message[..4].try_into()?) != 0 {
                    return Err("netlink dump failed".into());
                }
                done = true;
            }
            16 if message.len() >= 16 => {
                let index = i32::from_ne_bytes(message[4..8].try_into()?);
                if index <= 0 {
                    return Err("invalid interface index".into());
                }
                let attributes = &message[16..];
                let kind = attribute(attributes, 18)
                    .and_then(|info| attribute(info, 1))
                    .map(|bytes| {
                        crate::json_text::text(bytes)
                            .trim_end_matches('\0')
                            .to_owned()
                    })
                    .unwrap_or_default();
                let mac = attribute(attributes, 1)
                    .map(|bytes| {
                        bytes
                            .iter()
                            .map(|b| format!("{b:02x}"))
                            .collect::<Vec<_>>()
                            .join(":")
                    })
                    .unwrap_or_default();
                let mtu = attribute(attributes, 4)
                    .filter(|bytes| bytes.len() == 4)
                    .map(|bytes| i64::from(u32::from_ne_bytes(bytes.try_into().unwrap())))
                    .unwrap_or_default();
                result.insert(index as u32, Link { kind, mac, mtu });
            }
            _ => {}
        }
        let aligned = (length + 3) & !3;
        at = at.checked_add(aligned).ok_or("netlink length overflow")?;
        if at > data.len() && at - aligned + length != data.len() {
            return Err("short netlink padding".into());
        }
    }
    Ok(done)
}
pub(crate) fn native() -> Result<BTreeMap<u32, Link>, Error> {
    let raw = unsafe {
        libc::socket(
            libc::AF_NETLINK,
            libc::SOCK_RAW | libc::SOCK_CLOEXEC | libc::SOCK_NONBLOCK,
            libc::NETLINK_ROUTE,
        )
    };
    if raw < 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    let socket = unsafe { OwnedFd::from_raw_fd(raw) };
    let mut local: libc::sockaddr_nl = unsafe { std::mem::zeroed() };
    local.nl_family = libc::AF_NETLINK as u16;
    if unsafe {
        libc::bind(
            socket.as_raw_fd(),
            (&local as *const libc::sockaddr_nl).cast(),
            std::mem::size_of_val(&local) as u32,
        )
    } != 0
    {
        return Err(std::io::Error::last_os_error().into());
    }
    let mut request = [0u8; 32];
    request[..4].copy_from_slice(&32u32.to_ne_bytes());
    request[4..6].copy_from_slice(&18u16.to_ne_bytes());
    request[6..8].copy_from_slice(&0x301u16.to_ne_bytes());
    request[8..12].copy_from_slice(&1u32.to_ne_bytes());
    if unsafe {
        libc::sendto(
            socket.as_raw_fd(),
            request.as_ptr().cast(),
            request.len(),
            0,
            (&local as *const libc::sockaddr_nl).cast(),
            std::mem::size_of_val(&local) as u32,
        )
    } != 32
    {
        return Err(std::io::Error::last_os_error().into());
    }
    let deadline = Instant::now() + Duration::from_secs(1);
    let mut result = BTreeMap::new();
    let mut buffer = vec![0u8; 65536];
    for _ in 0..1024 {
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err("netlink receive timeout".into());
        }
        let mut ready = libc::pollfd {
            fd: socket.as_raw_fd(),
            events: libc::POLLIN,
            revents: 0,
        };
        let millis = remaining.as_millis().clamp(1, 1000) as i32;
        let status = unsafe { libc::poll(&mut ready, 1, millis) };
        if status < 0 {
            let error = std::io::Error::last_os_error();
            if error.kind() == std::io::ErrorKind::Interrupted {
                continue;
            }
            return Err(error.into());
        }
        if status == 0 {
            continue;
        }
        let mut sender: libc::sockaddr_nl = unsafe { std::mem::zeroed() };
        let mut length = std::mem::size_of_val(&sender) as libc::socklen_t;
        let received = unsafe {
            libc::recvfrom(
                socket.as_raw_fd(),
                buffer.as_mut_ptr().cast(),
                buffer.len(),
                libc::MSG_TRUNC,
                (&mut sender as *mut libc::sockaddr_nl).cast(),
                &mut length,
            )
        };
        if received < 0 {
            let error = std::io::Error::last_os_error();
            if matches!(
                error.kind(),
                std::io::ErrorKind::WouldBlock | std::io::ErrorKind::Interrupted
            ) {
                continue;
            }
            return Err(error.into());
        }
        if received == 0
            || received as usize > buffer.len()
            || length as usize != std::mem::size_of_val(&sender)
            || sender.nl_family != libc::AF_NETLINK as u16
            || sender.nl_pid != 0
        {
            return Err("invalid netlink kernel response".into());
        }
        if parse_links(&buffer[..received as usize], &mut result)? {
            return Ok(result);
        }
    }
    Err("netlink message limit".into())
}

#[cfg(test)]
mod tests {
    use super::*;
    use base64::{engine::general_purpose::STANDARD, Engine};
    fn attr(kind: u16, payload: &[u8]) -> Vec<u8> {
        let length = 4 + payload.len();
        let mut result = vec![0; (length + 3) & !3];
        result[..2].copy_from_slice(&(length as u16).to_ne_bytes());
        result[2..4].copy_from_slice(&kind.to_ne_bytes());
        result[4..length].copy_from_slice(payload);
        result
    }
    fn message(kind: u16, payload: &[u8]) -> Vec<u8> {
        let length = 16 + payload.len();
        let mut result = vec![0; (length + 3) & !3];
        result[..4].copy_from_slice(&(length as u32).to_ne_bytes());
        result[4..6].copy_from_slice(&kind.to_ne_bytes());
        result[8..12].copy_from_slice(&1u32.to_ne_bytes());
        result[16..length].copy_from_slice(payload);
        result
    }
    #[test]
    fn go_attributes_nested_link_metadata_and_dump_completion() {
        let oracle: serde_json::Value = serde_json::from_str(include_str!(
            "../../docs/experiments/v3.0/dashboard-go-oracle.json"
        ))
        .unwrap();
        for case in oracle["attributes"].as_array().unwrap() {
            let data = STANDARD.decode(case["data"].as_str().unwrap()).unwrap();
            assert_eq!(
                attribute(&data, 1).unwrap_or_default(),
                STANDARD.decode(case["value"].as_str().unwrap()).unwrap()
            )
        }
        let mut data = vec![0; 16];
        data[4..8].copy_from_slice(&7i32.to_ne_bytes());
        data.extend(attr(4, &1500u32.to_ne_bytes()));
        data.extend(attr(1, &[2, 0, 0, 0, 0, 1]));
        data.extend(attr(18 | 0x8000, &attr(1, b"wireguard\0")));
        let mut result = BTreeMap::new();
        assert!(!parse_links(&message(16, &data), &mut result).unwrap());
        assert_eq!(result[&7].kind, "wireguard");
        assert_eq!(result[&7].mac, "02:00:00:00:00:01");
        assert_eq!(result[&7].mtu, 1500);
        assert!(parse_links(&message(3, &[0; 4]), &mut result).unwrap());
    }
    #[test]
    fn netlink_framing_errors_do_not_produce_metadata() {
        for data in [
            vec![0; 15],
            vec![0; 16],
            message(2, &[0; 4]),
            message(3, &(-1i32).to_ne_bytes()),
        ] {
            assert!(parse_links(&data, &mut BTreeMap::new()).is_err())
        }
        let mut sequence = message(3, &[0; 4]);
        sequence[8..12].copy_from_slice(&2u32.to_ne_bytes());
        assert!(parse_links(&sequence, &mut BTreeMap::new()).is_err());
        let mut interrupted = message(3, &[0; 4]);
        interrupted[6..8].copy_from_slice(&0x10u16.to_ne_bytes());
        assert!(parse_links(&interrupted, &mut BTreeMap::new()).is_err());
        let mut short = message(16, &[0; 16]);
        short[..4].copy_from_slice(&500u32.to_ne_bytes());
        assert!(parse_links(&short, &mut BTreeMap::new()).is_err());
    }
}
