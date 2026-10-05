//! Go's JSON decoder syntax diagnostics for the first value.
//! Independent of numeric range: ignored huge numbers remain valid JSON.
use crate::Error;
struct Parser<'a> {
    bytes: &'a [u8],
    at: usize,
}
fn quoted(byte: u8) -> String {
    match byte {
        b'\n' => "'\\n'".into(),
        b'\r' => "'\\r'".into(),
        b'\t' => "'\\t'".into(),
        b'\\' => "'\\\\'".into(),
        b'\'' => "'\\\''".into(),
        0..=31 | 127 => format!("'\\x{byte:02x}'"),
        _ => format!("'{}'", char::from(byte)),
    }
}
impl Parser<'_> {
    fn ws(&mut self) {
        while self
            .bytes
            .get(self.at)
            .is_some_and(|b| matches!(b, b' ' | b'\n' | b'\r' | b'\t'))
        {
            self.at += 1;
        }
    }
    fn peek(&self) -> Result<u8, Error> {
        self.bytes
            .get(self.at)
            .copied()
            .ok_or_else(|| "unexpected EOF".into())
    }
    fn invalid(&self, context: &str) -> Error {
        match self.bytes.get(self.at) {
            Some(byte) => format!("invalid character {} {context}", quoted(*byte)).into(),
            None => "unexpected EOF".into(),
        }
    }
    fn string(&mut self) -> Result<(), Error> {
        self.at += 1;
        loop {
            match self.peek()? {
                b'"' => {
                    self.at += 1;
                    return Ok(());
                }
                b'\\' => {
                    let escape_start = self.at;
                    self.at += 1;
                    match self.peek()? {
                        b'"' | b'\\' | b'/' | b'b' | b'f' | b'n' | b'r' | b't' => self.at += 1,
                        b'u' => {
                            self.at += 1;
                            for _ in 0..4 {
                                if !self.peek()?.is_ascii_hexdigit() {
                                    let end = (escape_start + 6).min(self.bytes.len());
                                    return Err(format!(
                                        "invalid escape sequence `{}` in string",
                                        String::from_utf8_lossy(&self.bytes[escape_start..end])
                                    )
                                    .into());
                                }
                                self.at += 1;
                            }
                        }
                        _ => {
                            return Err(format!(
                                "invalid escape sequence `{}` in string",
                                String::from_utf8_lossy(&self.bytes[escape_start..self.at + 1])
                            )
                            .into())
                        }
                    }
                }
                0..=31 => return Err(self.invalid("in string literal")),
                _ => self.at += 1,
            }
        }
    }
    fn value(&mut self, stack: &mut Vec<State>) -> Result<(), Error> {
        self.ws();
        match self.peek()? {
            b'"' => self.string(),
            byte @ (b'{' | b'[') => {
                if stack.len() >= 10000 {
                    return Err("exceeded max depth".into());
                }
                self.at += 1;
                stack.push(if byte == b'{' {
                    State::ObjectFirst
                } else {
                    State::ArrayFirst
                });
                Ok(())
            }
            byte @ (b't' | b'f' | b'n') => {
                let token: &[u8] = match byte {
                    b't' => b"true",
                    b'f' => b"false",
                    _ => b"null",
                };
                for expected in token {
                    if self.peek()? != *expected {
                        return Err(self.invalid(&format!(
                            "in literal {} (expecting '{}')",
                            std::str::from_utf8(token).unwrap(),
                            char::from(*expected)
                        )));
                    }
                    self.at += 1;
                }
                Ok(())
            }
            b'-' | b'0'..=b'9' => self.number(),
            _ => Err(self.invalid("looking for beginning of value")),
        }
    }
    fn number(&mut self) -> Result<(), Error> {
        if self.peek()? == b'-' {
            self.at += 1;
            if !self.peek()?.is_ascii_digit() {
                return Err(self.invalid("in numeric literal"));
            }
        }
        if self.peek()? == b'0' {
            self.at += 1;
        } else {
            while self.bytes.get(self.at).is_some_and(u8::is_ascii_digit) {
                self.at += 1;
            }
        }
        if self.bytes.get(self.at) == Some(&b'.') {
            self.at += 1;
            if !self.peek()?.is_ascii_digit() {
                return Err(self.invalid("in numeric literal"));
            }
            while self.bytes.get(self.at).is_some_and(u8::is_ascii_digit) {
                self.at += 1;
            }
        }
        if self
            .bytes
            .get(self.at)
            .is_some_and(|b| matches!(b, b'e' | b'E'))
        {
            self.at += 1;
            if self
                .bytes
                .get(self.at)
                .is_some_and(|b| matches!(b, b'+' | b'-'))
            {
                self.at += 1;
            }
            if !self.peek()?.is_ascii_digit() {
                return Err(self.invalid("in numeric literal"));
            }
            while self.bytes.get(self.at).is_some_and(u8::is_ascii_digit) {
                self.at += 1;
            }
        }
        Ok(())
    }
    fn first(&mut self) -> Result<(), Error> {
        self.ws();
        if self.at == self.bytes.len() {
            return Err("EOF".into());
        }
        let mut stack = Vec::new();
        self.value(&mut stack)?;
        while let Some(state) = stack.last().copied() {
            self.ws();
            let byte = self.peek()?;
            match state {
                State::ObjectFirst if byte == b'}' => {
                    self.at += 1;
                    stack.pop();
                }
                State::ObjectFirst | State::ObjectKey => {
                    if byte != b'"' {
                        return Err(self.invalid("looking for beginning of object key string"));
                    }
                    self.string()?;
                    self.ws();
                    if self.peek()? != b':' {
                        return Err(self.invalid("after object key"));
                    }
                    self.at += 1;
                    *stack.last_mut().unwrap() = State::ObjectAfter;
                    self.value(&mut stack)?;
                }
                State::ArrayFirst if byte == b']' => {
                    self.at += 1;
                    stack.pop();
                }
                State::ArrayFirst | State::ArrayValue => {
                    *stack.last_mut().unwrap() = State::ArrayAfter;
                    self.value(&mut stack)?;
                }
                State::ObjectAfter | State::ArrayAfter => {
                    let object = matches!(state, State::ObjectAfter);
                    let end = if object { b'}' } else { b']' };
                    if byte == end {
                        self.at += 1;
                        stack.pop();
                    } else if byte == b',' {
                        self.at += 1;
                        *stack.last_mut().unwrap() = if object {
                            State::ObjectKey
                        } else {
                            State::ArrayValue
                        };
                    } else {
                        return Err(self.invalid(if object {
                            "after object key:value pair"
                        } else {
                            "after array element"
                        }));
                    }
                }
            }
        }
        Ok(())
    }
}
#[derive(Clone, Copy)]
enum State {
    ObjectFirst,
    ObjectKey,
    ObjectAfter,
    ArrayFirst,
    ArrayValue,
    ArrayAfter,
}
pub(crate) fn first_value(bytes: &[u8]) -> Result<(), Error> {
    Parser { bytes, at: 0 }.first()
}
