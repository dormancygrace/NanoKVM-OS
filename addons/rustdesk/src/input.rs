// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::collections::BTreeSet;

use crate::{
    onekvm::HidClient,
    protocol::{key_event, ControlKey, KeyEvent, KeyboardMode, MouseEvent},
};

const MOUSE_TYPE_MASK: i32 = 0x07;
const MOUSE_TYPE_MOVE: i32 = 0;
const MOUSE_TYPE_DOWN: i32 = 1;
const MOUSE_TYPE_UP: i32 = 2;
const MOUSE_TYPE_WHEEL: i32 = 3;
const MOUSE_TYPE_TRACKPAD: i32 = 4;
const MOUSE_TYPE_MOVE_RELATIVE: i32 = 5;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum PointerMode {
    Absolute,
    Relative,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum HidKey {
    Modifier(u8),
    Usage(u8),
}

pub struct InputState {
    hid: HidClient,
    width: u16,
    height: u16,
    modifiers: u8,
    keys: BTreeSet<u8>,
    buttons: u16,
    x: u16,
    y: u16,
    pointer_mode: PointerMode,
    pointer_mode_announced: bool,
}

impl InputState {
    pub fn new(hid: HidClient, width: u16, height: u16) -> Self {
        Self {
            hid,
            width,
            height,
            modifiers: 0,
            keys: BTreeSet::new(),
            buttons: 0,
            x: 0,
            y: 0,
            pointer_mode: PointerMode::Absolute,
            pointer_mode_announced: false,
        }
    }

    pub async fn handle_mouse(&mut self, event: MouseEvent) -> std::io::Result<()> {
        let event_type = event.mask & MOUSE_TYPE_MASK;
        let button = (event.mask >> 3) as u16;
        match event_type {
            MOUSE_TYPE_MOVE => {
                self.switch_pointer_mode(PointerMode::Absolute).await?;
                self.x = scale_coordinate(event.x, self.width);
                self.y = scale_coordinate(event.y, self.height);
                self.hid.absolute_mouse(self.buttons, self.x, self.y).await
            }
            MOUSE_TYPE_MOVE_RELATIVE => {
                self.switch_pointer_mode(PointerMode::Relative).await?;
                let x = clamp_i8(event.x);
                let y = clamp_i8(event.y);
                self.hid.mouse(self.buttons as u8, x, y, 0).await
            }
            MOUSE_TYPE_DOWN => {
                self.buttons |= button;
                self.send_pointer_buttons().await
            }
            MOUSE_TYPE_UP => {
                self.buttons &= !button;
                self.send_pointer_buttons().await
            }
            MOUSE_TYPE_WHEEL | MOUSE_TYPE_TRACKPAD => {
                let wheel = clamp_i8(event.y.saturating_neg());
                match self.pointer_mode {
                    PointerMode::Absolute => {
                        self.hid
                            .absolute_mouse_wheel(self.buttons, self.x, self.y, wheel)
                            .await
                    }
                    PointerMode::Relative => self.hid.mouse(self.buttons as u8, 0, 0, wheel).await,
                }
            }
            _ => Ok(()),
        }
    }

    async fn switch_pointer_mode(&mut self, mode: PointerMode) -> std::io::Result<()> {
        if self.pointer_mode == mode {
            self.announce_pointer_mode();
            return Ok(());
        }
        match self.pointer_mode {
            PointerMode::Absolute => self.hid.absolute_mouse(0, self.x, self.y).await?,
            PointerMode::Relative => self.hid.mouse(0, 0, 0, 0).await?,
        }
        self.pointer_mode = mode;
        self.pointer_mode_announced = false;
        self.announce_pointer_mode();
        Ok(())
    }

    fn announce_pointer_mode(&mut self) {
        if self.pointer_mode_announced {
            return;
        }
        let mode = match self.pointer_mode {
            PointerMode::Absolute => "absolute",
            PointerMode::Relative => "relative",
        };
        eprintln!("RustDesk pointer mode switched to {mode}");
        self.pointer_mode_announced = true;
    }

    async fn send_pointer_buttons(&self) -> std::io::Result<()> {
        match self.pointer_mode {
            PointerMode::Absolute => self.hid.absolute_mouse(self.buttons, self.x, self.y).await,
            PointerMode::Relative => self.hid.mouse(self.buttons as u8, 0, 0, 0).await,
        }
    }

    pub async fn handle_key(&mut self, event: KeyEvent) -> std::io::Result<()> {
        let mode = KeyboardMode::try_from(event.mode).unwrap_or(KeyboardMode::Legacy);
        // Map events, and Chr fallbacks in Translate mode, carry Linux physical
        // keycodes. Their modifier list only describes lock state, so preserve
        // modifiers tracked from the physical key down/up events.
        if !matches!(mode, KeyboardMode::Map | KeyboardMode::Translate) {
            self.modifiers = modifiers_from_control_keys(&event.modifiers);
        }
        if let Some(key_event::Union::ControlKey(value)) = event.union {
            let key = ControlKey::try_from(value).unwrap_or(ControlKey::Unknown);
            if key == ControlKey::CtrlAltDel {
                return self.ctrl_alt_delete().await;
            }
            if let Some(bit) = modifier_bit(key) {
                self.apply_modifier(bit, event.down || event.press);
                self.send_keyboard().await?;
                if event.press {
                    self.apply_modifier(bit, false);
                    self.send_keyboard().await?;
                }
                return Ok(());
            }
            if let Some(usage) = control_key_usage(key) {
                return self.apply_usage(usage, event.down, event.press, 0).await;
            }
            return Ok(());
        }

        let (usage, implied_modifier) = match event.union {
            Some(key_event::Union::Chr(value))
                if matches!(mode, KeyboardMode::Map | KeyboardMode::Translate) =>
            {
                let Some(key) = linux_xorg_keycode_to_hid(value) else {
                    return Ok(());
                };
                match key {
                    HidKey::Modifier(bit) => {
                        self.apply_modifier(bit, event.down || event.press);
                        self.send_keyboard().await?;
                        if event.press {
                            self.apply_modifier(bit, false);
                            self.send_keyboard().await?;
                        }
                        return Ok(());
                    }
                    HidKey::Usage(usage) => (Some(usage), 0),
                }
            }
            Some(key_event::Union::Chr(value)) => char_to_hid(char::from_u32(value)),
            Some(key_event::Union::Unicode(value)) => char_to_hid(char::from_u32(value)),
            Some(key_event::Union::Seq(sequence)) if sequence.chars().count() == 1 => {
                char_to_hid(sequence.chars().next())
            }
            _ => (None, 0),
        };
        if let Some(usage) = usage {
            self.apply_usage(usage, event.down, event.press, implied_modifier)
                .await?;
        }
        Ok(())
    }

    pub async fn heartbeat(&self) -> std::io::Result<()> {
        self.hid.heartbeat().await
    }

    pub async fn release_all(&mut self) {
        self.modifiers = 0;
        self.keys.clear();
        self.buttons = 0;
        let _ = self.send_keyboard().await;
        let _ = self.hid.absolute_mouse(0, self.x, self.y).await;
        let _ = self.hid.mouse(0, 0, 0, 0).await;
        self.hid.close().await;
    }

    async fn apply_usage(
        &mut self,
        usage: u8,
        down: bool,
        press: bool,
        implied_modifier: u8,
    ) -> std::io::Result<()> {
        if down || press {
            self.modifiers |= implied_modifier;
            self.keys.insert(usage);
            self.send_keyboard().await?;
        } else {
            self.keys.remove(&usage);
            self.send_keyboard().await?;
        }
        if press {
            self.keys.remove(&usage);
            self.modifiers &= !implied_modifier;
            self.send_keyboard().await?;
        }
        Ok(())
    }

    fn apply_modifier(&mut self, bit: u8, down: bool) {
        if down {
            self.modifiers |= bit;
        } else {
            self.modifiers &= !bit;
        }
    }

    async fn send_keyboard(&self) -> std::io::Result<()> {
        let keys: Vec<u8> = self.keys.iter().copied().take(6).collect();
        self.hid.keyboard(self.modifiers, &keys).await
    }

    async fn ctrl_alt_delete(&mut self) -> std::io::Result<()> {
        let previous_modifiers = self.modifiers;
        self.modifiers = 0x01 | 0x04;
        self.keys.insert(0x4c);
        self.send_keyboard().await?;
        self.keys.remove(&0x4c);
        self.modifiers = previous_modifiers;
        self.send_keyboard().await
    }
}

fn clamp_i8(value: i32) -> i8 {
    value.clamp(-127, 127) as i8
}

fn scale_coordinate(value: i32, extent: u16) -> u16 {
    if extent <= 1 {
        return 0;
    }
    let maximum = i32::from(extent) - 1;
    ((value.clamp(0, maximum) as u32 * 32767) / maximum as u32) as u16
}

fn modifiers_from_control_keys(keys: &[i32]) -> u8 {
    keys.iter()
        .filter_map(|value| ControlKey::try_from(*value).ok())
        .filter_map(modifier_bit)
        .fold(0, |result, value| result | value)
}

fn modifier_bit(key: ControlKey) -> Option<u8> {
    match key {
        ControlKey::Control => Some(0x01),
        ControlKey::Shift => Some(0x02),
        ControlKey::Alt | ControlKey::Option => Some(0x04),
        ControlKey::Meta => Some(0x08),
        ControlKey::RControl => Some(0x10),
        ControlKey::RShift => Some(0x20),
        ControlKey::RAlt => Some(0x40),
        ControlKey::RWin => Some(0x80),
        _ => None,
    }
}

fn control_key_usage(key: ControlKey) -> Option<u8> {
    Some(match key {
        ControlKey::Backspace => 0x2a,
        ControlKey::CapsLock => 0x39,
        ControlKey::Delete => 0x4c,
        ControlKey::DownArrow => 0x51,
        ControlKey::End => 0x4d,
        ControlKey::Escape => 0x29,
        ControlKey::F1 => 0x3a,
        ControlKey::F2 => 0x3b,
        ControlKey::F3 => 0x3c,
        ControlKey::F4 => 0x3d,
        ControlKey::F5 => 0x3e,
        ControlKey::F6 => 0x3f,
        ControlKey::F7 => 0x40,
        ControlKey::F8 => 0x41,
        ControlKey::F9 => 0x42,
        ControlKey::F10 => 0x43,
        ControlKey::F11 => 0x44,
        ControlKey::F12 => 0x45,
        ControlKey::Home => 0x4a,
        ControlKey::Insert => 0x49,
        ControlKey::LeftArrow => 0x50,
        ControlKey::PageDown => 0x4e,
        ControlKey::PageUp => 0x4b,
        ControlKey::Pause => 0x48,
        ControlKey::Return => 0x28,
        ControlKey::RightArrow => 0x4f,
        ControlKey::Snapshot => 0x46,
        ControlKey::Space => 0x2c,
        ControlKey::Scroll => 0x47,
        ControlKey::Tab => 0x2b,
        ControlKey::UpArrow => 0x52,
        ControlKey::NumLock => 0x53,
        ControlKey::Numpad0 => 0x62,
        ControlKey::Numpad1 => 0x59,
        ControlKey::Numpad2 => 0x5a,
        ControlKey::Numpad3 => 0x5b,
        ControlKey::Numpad4 => 0x5c,
        ControlKey::Numpad5 => 0x5d,
        ControlKey::Numpad6 => 0x5e,
        ControlKey::Numpad7 => 0x5f,
        ControlKey::Numpad8 => 0x60,
        ControlKey::Numpad9 => 0x61,
        ControlKey::Multiply => 0x55,
        ControlKey::Add => 0x57,
        ControlKey::Subtract => 0x56,
        ControlKey::Decimal => 0x63,
        ControlKey::Divide => 0x54,
        ControlKey::Equals => 0x67,
        ControlKey::NumpadEnter => 0x58,
        ControlKey::Apps => 0x65,
        _ => return None,
    })
}

fn char_to_hid(value: Option<char>) -> (Option<u8>, u8) {
    let Some(value) = value else {
        return (None, 0);
    };
    if value.is_ascii_alphabetic() {
        return (
            Some(0x04 + value.to_ascii_lowercase() as u8 - b'a'),
            if value.is_ascii_uppercase() { 0x02 } else { 0 },
        );
    }
    if let Some(position) = "1234567890".find(value) {
        return (Some(0x1e + position as u8), 0);
    }
    let (usage, shift) = match value {
        '\n' | '\r' => (0x28, false),
        '\t' => (0x2b, false),
        ' ' => (0x2c, false),
        '-' => (0x2d, false),
        '_' => (0x2d, true),
        '=' => (0x2e, false),
        '+' => (0x2e, true),
        '[' => (0x2f, false),
        '{' => (0x2f, true),
        ']' => (0x30, false),
        '}' => (0x30, true),
        '\\' => (0x31, false),
        '|' => (0x31, true),
        ';' => (0x33, false),
        ':' => (0x33, true),
        '\'' => (0x34, false),
        '"' => (0x34, true),
        '`' => (0x35, false),
        '~' => (0x35, true),
        ',' => (0x36, false),
        '<' => (0x36, true),
        '.' => (0x37, false),
        '>' => (0x37, true),
        '/' => (0x38, false),
        '?' => (0x38, true),
        '!' => (0x1e, true),
        '@' => (0x1f, true),
        '#' => (0x20, true),
        '$' => (0x21, true),
        '%' => (0x22, true),
        '^' => (0x23, true),
        '&' => (0x24, true),
        '*' => (0x25, true),
        '(' => (0x26, true),
        ')' => (0x27, true),
        _ => return (None, 0),
    };
    (Some(usage), if shift { 0x02 } else { 0 })
}

fn linux_xorg_keycode_to_hid(value: u32) -> Option<HidKey> {
    let evdev = value.checked_sub(8)?;
    let modifier = match evdev {
        29 => Some(0x01),
        42 => Some(0x02),
        56 => Some(0x04),
        125 => Some(0x08),
        97 => Some(0x10),
        54 => Some(0x20),
        100 => Some(0x40),
        126 => Some(0x80),
        _ => None,
    };
    if let Some(modifier) = modifier {
        return Some(HidKey::Modifier(modifier));
    }
    let usage = match evdev {
        1 => 0x29,
        2..=11 => 0x1e + ((evdev + 8) % 10) as u8,
        12 => 0x2d,
        13 => 0x2e,
        14 => 0x2a,
        15 => 0x2b,
        16..=25 => 0x14 + (evdev - 16) as u8,
        26 => 0x2f,
        27 => 0x30,
        28 => 0x28,
        30..=38 => 0x04 + (evdev - 30) as u8,
        39 => 0x33,
        40 => 0x34,
        41 => 0x35,
        43 => 0x31,
        44 => 0x1d,
        45 => 0x1b,
        46 => 0x06,
        47 => 0x19,
        48 => 0x05,
        49 => 0x11,
        50 => 0x10,
        51 => 0x36,
        52 => 0x37,
        53 => 0x38,
        57 => 0x2c,
        58 => 0x39,
        59..=68 => 0x3a + (evdev - 59) as u8,
        69 => 0x53,
        70 => 0x47,
        71..=73 => 0x5f + (evdev - 71) as u8,
        74 => 0x56,
        75..=77 => 0x5c + (evdev - 75) as u8,
        78 => 0x57,
        79..=81 => 0x59 + (evdev - 79) as u8,
        82 => 0x62,
        83 => 0x63,
        86 => 0x64,
        87 => 0x44,
        88 => 0x45,
        96 => 0x58,
        98 => 0x54,
        99 => 0x46,
        102 => 0x4a,
        103 => 0x52,
        104 => 0x4b,
        105 => 0x50,
        106 => 0x4f,
        107 => 0x4d,
        108 => 0x51,
        109 => 0x4e,
        110 => 0x49,
        111 => 0x4c,
        117 => 0x67,
        119 => 0x48,
        121 => 0x85,
        127 => 0x65,
        _ => return None,
    };
    Some(HidKey::Usage(usage))
}

#[cfg(test)]
mod tests {
    use super::{char_to_hid, linux_xorg_keycode_to_hid, scale_coordinate, HidKey};

    #[test]
    fn scales_absolute_coordinates() {
        assert_eq!(scale_coordinate(0, 1920), 0);
        assert_eq!(scale_coordinate(1919, 1920), 32767);
        assert_eq!(scale_coordinate(960, 1920), 16392);
    }

    #[test]
    fn maps_xorg_keycodes() {
        assert_eq!(linux_xorg_keycode_to_hid(38), Some(HidKey::Usage(0x04)));
        assert_eq!(linux_xorg_keycode_to_hid(36), Some(HidKey::Usage(0x28)));
        assert_eq!(linux_xorg_keycode_to_hid(94), Some(HidKey::Usage(0x64)));
    }

    #[test]
    fn maps_xorg_modifier_keycodes() {
        assert_eq!(linux_xorg_keycode_to_hid(37), Some(HidKey::Modifier(0x01)));
        assert_eq!(linux_xorg_keycode_to_hid(50), Some(HidKey::Modifier(0x02)));
        assert_eq!(linux_xorg_keycode_to_hid(108), Some(HidKey::Modifier(0x40)));
        assert_eq!(linux_xorg_keycode_to_hid(134), Some(HidKey::Modifier(0x80)));
    }

    #[test]
    fn maps_printable_characters() {
        assert_eq!(char_to_hid(Some('a')), (Some(0x04), 0));
        assert_eq!(char_to_hid(Some('A')), (Some(0x04), 0x02));
        assert_eq!(char_to_hid(Some('?')), (Some(0x38), 0x02));
    }
}
