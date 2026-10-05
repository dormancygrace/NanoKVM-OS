//! Owned nonblocking update lock shared with the firmware APK/image helpers.
use crate::{fsroot, Error};
use std::{
    ffi::CString,
    fs::{self, File},
    os::{
        fd::{AsRawFd, FromRawFd},
        unix::{ffi::OsStrExt, fs::DirBuilderExt},
    },
    path::Path,
};
#[derive(Debug)]
pub struct Lock {
    _file: File,
}
impl Lock {
    pub fn acquire(root: &Path) -> Result<Self, Error> {
        let parent = fsroot::resolve(root, Path::new("/kvmapp"), true)?;
        if !parent.exists() {
            fs::DirBuilder::new()
                .mode(0o700)
                .create(&parent)
                .or_else(|e| {
                    if e.kind() == std::io::ErrorKind::AlreadyExists {
                        Ok(())
                    } else {
                        Err(e)
                    }
                })?;
        }
        let base = parent.join(".os-update");
        match fs::symlink_metadata(&base) {
            Ok(m) if !m.is_dir() || m.file_type().is_symlink() => {
                return Err("unsafe update state directory".into())
            }
            Ok(_) => {}
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                fs::DirBuilder::new()
                    .mode(0o700)
                    .create(&base)
                    .or_else(|e| {
                        if e.kind() == std::io::ErrorKind::AlreadyExists {
                            Ok(())
                        } else {
                            Err(e)
                        }
                    })?;
            }
            Err(e) => return Err(e.into()),
        }
        let name = CString::new(base.as_os_str().as_bytes())?;
        let fd = unsafe {
            libc::open(
                name.as_ptr(),
                libc::O_RDONLY | libc::O_DIRECTORY | libc::O_CLOEXEC | libc::O_NOFOLLOW,
            )
        };
        if fd < 0 {
            return Err("unsafe update state directory".into());
        }
        let dir = unsafe { File::from_raw_fd(fd) };
        let fd = unsafe {
            libc::openat(
                dir.as_raw_fd(),
                c"lock".as_ptr(),
                libc::O_CREAT
                    | libc::O_RDWR
                    | libc::O_CLOEXEC
                    | libc::O_NOFOLLOW
                    | libc::O_NONBLOCK,
                0o600,
            )
        };
        if fd < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        let file = unsafe { File::from_raw_fd(fd) };
        if !file.metadata()?.is_file() {
            return Err("unsafe update lock".into());
        }
        if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } != 0 {
            return Err("another update operation is in progress".into());
        }
        Ok(Self { _file: file })
    }
}
