//! Bounded local daemon status, never a clock-setting operation.
use crate::Error;
use std::{
    net::{SocketAddr, UdpSocket},
    sync::atomic::{AtomicU32, Ordering},
    time::{Duration, Instant},
};
static SEQUENCE: AtomicU32 = AtomicU32::new(0);
pub fn parse_ntp(packet: &[u8], sequence: u16) -> Result<bool, Error> {
    let word = |offset| u16::from_be_bytes([packet[offset], packet[offset + 1]]);
    if packet.len() < 12
        || packet[0] & 7 != 6
        || (packet[0] >> 3) & 7 < 2
        || packet[1] & 0x80 == 0
        || packet[1] & 0x40 != 0
        || packet[1] & 0x1f != 2
        || word(2) != sequence
        || word(6) != 0
        || word(8) != 0
    {
        return Err("invalid local NTP status reply".into());
    }
    if usize::from(word(10)) > packet.len() - 12 {
        return Err("truncated local NTP status reply".into());
    }
    let status = word(4);
    Ok(status >> 14 != 3 && (status >> 8) & 0x3f == 6)
}
pub fn ntp_at(address: SocketAddr, timeout: Duration) -> Result<bool, Error> {
    let socket = UdpSocket::bind("127.0.0.1:0")?;
    socket.connect(address)?;
    let deadline = Instant::now()
        .checked_add(timeout)
        .ok_or("invalid local NTP timeout")?;
    if timeout.is_zero() {
        return Err("local NTP status timed out".into());
    }
    socket.set_write_timeout(Some(timeout))?;
    let sequence = SEQUENCE.fetch_add(1, Ordering::Relaxed).wrapping_add(1) as u16;
    let mut request = [0; 12];
    request[0] = 4 << 3 | 6;
    request[1] = 2;
    request[2..4].copy_from_slice(&sequence.to_be_bytes());
    socket.send(&request)?;
    let remaining = deadline.saturating_duration_since(Instant::now());
    if remaining.is_zero() {
        return Err("local NTP status timed out".into());
    }
    socket.set_read_timeout(Some(remaining))?;
    let mut response = [0; 2048];
    let count = socket.recv(&mut response)?;
    parse_ntp(&response[..count], sequence)
}
// chronyc tracking is one CSV record. Keep Go's strict quoting, doubled quotes,
// multiline quoted fields, CRLF normalization and rejection of bare quotes.
fn csv_record(text: &str) -> Result<Vec<String>, Error> {
    let text = text.trim().replace("\r\n", "\n");
    let bytes = text.as_bytes();
    let mut offset = 0;
    let mut fields = Vec::new();
    while offset < bytes.len() {
        let mut field = Vec::new();
        if bytes[offset] == b'"' {
            offset += 1;
            loop {
                if offset == bytes.len() {
                    return Err("invalid chrony tracking response".into());
                }
                let b = bytes[offset];
                offset += 1;
                if b == b'"' {
                    if bytes.get(offset) == Some(&b'"') {
                        field.push(b'"');
                        offset += 1;
                    } else {
                        break;
                    }
                } else {
                    field.push(b);
                }
            }
        } else {
            while offset < bytes.len() && !matches!(bytes[offset], b',' | b'\n') {
                if bytes[offset] == b'"' {
                    return Err("invalid chrony tracking response".into());
                }
                field.push(bytes[offset]);
                offset += 1;
            }
        }
        fields.push(String::from_utf8(field)?);
        match bytes.get(offset) {
            None => break,
            Some(b',') => {
                offset += 1;
                if offset == bytes.len() {
                    fields.push(String::new());
                    break;
                }
            }
            Some(b'\n') => {
                if bytes[offset..].iter().all(|b| *b == b'\n') {
                    break;
                }
                return Err("invalid chrony tracking response".into());
            }
            _ => return Err("invalid chrony tracking response".into()),
        }
    }
    Ok(fields)
}
pub fn parse_chrony(text: &str) -> Result<bool, Error> {
    let fields = csv_record(text)?;
    if fields.len() != 14 {
        return Err("invalid chrony tracking response".into());
    }
    let stratum = fields[2]
        .parse::<i64>()
        .map_err(|_| "invalid chrony stratum")?;
    if !(1..=15).contains(&stratum) || fields[0] == "00000000" || fields[0] == "7F7F0101" {
        return Ok(false);
    }
    match fields[13].as_str() {
        "Normal" | "Insert second" | "Delete second" => Ok(true),
        "Not synchronised" => Ok(false),
        _ => Err("unknown chrony leap status".into()),
    }
}
