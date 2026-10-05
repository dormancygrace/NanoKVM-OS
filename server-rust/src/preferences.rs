//! Atomic preference snapshots preserve final symlinks without changing targets.
use crate::{store::atomic_write, Error};
use std::{
    fs,
    os::unix::fs::{symlink, PermissionsExt},
    path::{Path, PathBuf},
};
pub(crate) enum Saved {
    Missing,
    File(Vec<u8>, u32),
    Link(PathBuf),
}
pub(crate) fn snapshot(path: &Path) -> Result<Saved, Error> {
    match fs::symlink_metadata(path) {
        Ok(meta) if meta.file_type().is_symlink() => Ok(Saved::Link(fs::read_link(path)?)),
        Ok(meta) if meta.is_file() => Ok(Saved::File(
            fs::read(path)?,
            meta.permissions().mode() & 0o7777,
        )),
        Ok(_) => Err("preference is not a regular file or symlink".into()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(Saved::Missing),
        Err(error) => Err(error.into()),
    }
}
pub(crate) fn restore(
    path: &Path,
    saved: &Saved,
    write: impl Fn(&Path, &[u8], u32) -> Result<(), Error>,
) -> Result<(), Error> {
    match saved {
        Saved::File(bytes, mode) => write(path, bytes, *mode),
        Saved::Missing => match fs::remove_file(path) {
            Ok(()) => Ok(fs::File::open(path.parent().unwrap())?.sync_all()?),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
            Err(error) => Err(error.into()),
        },
        Saved::Link(target) => {
            let mut random = [0; 16];
            getrandom::fill(&mut random)?;
            let name = random
                .iter()
                .map(|b| format!("{b:02x}"))
                .collect::<String>();
            let temporary = path
                .parent()
                .unwrap()
                .join(format!(".preference-restore-{name}"));
            symlink(target, &temporary)?;
            let result = (|| -> Result<(), Error> {
                fs::rename(&temporary, path)?;
                fs::File::open(path.parent().unwrap())?.sync_all()?;
                Ok(())
            })();
            let _ = fs::remove_file(temporary);
            result
        }
    }
}
// The write closure is injectable so rename-success/directory-sync-failure can
// be qualified without corrupting or mounting a real filesystem.
pub(crate) fn write(path: &Path, bytes: &[u8], mode: u32) -> Result<(), Error> {
    atomic_write(path, bytes, mode)
}
