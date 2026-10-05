//! Durable setting replacement with Go's existing-mode/new-umask semantics.
use crate::{fsroot, Error};
use std::{
    fs,
    io::Write,
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::Path,
};

pub(crate) fn write(
    root: &Path,
    logical: &str,
    data: &[u8],
    mode: u32,
    preserve: bool,
) -> Result<(), Error> {
    let logical_path = Path::new(logical);
    let path = if preserve {
        fsroot::resolve(root, logical_path, true)?
    } else {
        fsroot::resolve(
            root,
            logical_path.parent().ok_or("missing setting directory")?,
            false,
        )?
        .join(logical_path.file_name().ok_or("missing setting filename")?)
    };
    let metadata = if preserve {
        fs::metadata(&path)
    } else {
        fs::symlink_metadata(&path)
    };
    let previous = match metadata {
        Ok(metadata) if metadata.is_file() => Some(metadata.permissions().mode() & 0o7777),
        Ok(metadata) if !preserve && metadata.file_type().is_symlink() => None,
        Ok(_) => return Err(std::io::Error::from_raw_os_error(libc::EISDIR).into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
        Err(error) => return Err(error.into()),
    };
    let parent = path.parent().ok_or("missing setting directory")?;
    // No directory creation: a missing/broken firmware directory must fail.
    let mut entropy = [0u8; 16];
    getrandom::fill(&mut entropy)?;
    let suffix: String = entropy.iter().map(|b| format!("{b:02x}")).collect();
    let temporary = parent.join(format!(".v3-screen-{suffix}"));
    let result = (|| -> Result<(), Error> {
        let mut file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(mode)
            .open(&temporary)?;
        if let Some(previous) = previous.filter(|_| preserve) {
            file.set_permissions(fs::Permissions::from_mode(previous))?;
        }
        file.write_all(data)?;
        file.sync_all()?;
        drop(file);
        fs::rename(&temporary, &path)?;
        fs::File::open(parent)?.sync_all()?;
        Ok(())
    })();
    let _ = fs::remove_file(&temporary);
    result
}

pub(crate) fn file_error(operation: &str, logical: &str, error: &Error) -> String {
    let message = match error.downcast_ref::<std::io::Error>() {
        Some(error) => error
            .to_string()
            .split(" (os error")
            .next()
            .unwrap_or("I/O error")
            .to_lowercase(),
        None => error.to_string(),
    };
    format!("{operation} {logical}: {message}")
}
