//! Resolve firmware paths inside a virtual root, including Alpine absolute
//! compatibility symlinks. Fixed privileged paths never escape an isolated root.
use crate::Error;
use std::{
    collections::VecDeque,
    fs,
    path::{Component, Path, PathBuf},
};
pub fn resolve(root: &Path, path: &Path, missing_final: bool) -> Result<PathBuf, Error> {
    let root = root.canonicalize()?;
    let relative = path.strip_prefix(&root).unwrap_or(path);
    let mut pending: VecDeque<_> = relative
        .components()
        .map(|part| part.as_os_str().to_os_string())
        .collect();
    let mut result = root.clone();
    let mut followed = 0usize;
    while let Some(part) = pending.pop_front() {
        let part_path = Path::new(&part);
        match part_path.components().next() {
            Some(Component::RootDir | Component::CurDir) | None => continue,
            Some(Component::ParentDir) => {
                if result == root {
                    return Err("path outside runtime root".into());
                }
                result.pop();
                continue;
            }
            Some(Component::Normal(_)) => result.push(part),
            _ => return Err("invalid runtime path".into()),
        }
        let metadata = match fs::symlink_metadata(&result) {
            Ok(metadata) => metadata,
            Err(error)
                if error.kind() == std::io::ErrorKind::NotFound
                    && missing_final
                    && pending.is_empty() =>
            {
                return Ok(result)
            }
            Err(error) => return Err(error.into()),
        };
        if metadata.file_type().is_symlink() {
            followed += 1;
            if followed > 40 {
                return Err("too many runtime symlinks".into());
            }
            let target = fs::read_link(&result)?;
            result.pop();
            let target = if target.is_absolute() {
                result = root.clone();
                target.strip_prefix(&root).unwrap_or(&target)
            } else {
                &target
            };
            for part in target.components().rev() {
                pending.push_front(part.as_os_str().to_os_string());
            }
        }
    }
    Ok(result)
}
#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;
    #[test]
    fn absolute_alpine_links_relative_links_missing_targets_and_escape_fail_closed() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        fs::create_dir_all(root.join("etc/init.d")).unwrap();
        fs::create_dir_all(root.join("usr/libexec/nanokvm/legacy")).unwrap();
        let target = root.join("usr/libexec/nanokvm/legacy/S03usbdev");
        fs::write(&target, "old").unwrap();
        symlink(
            "/usr/libexec/nanokvm/legacy/S03usbdev",
            root.join("etc/init.d/S03usbdev"),
        )
        .unwrap();
        assert_eq!(
            resolve(root, Path::new("/etc/init.d/S03usbdev"), false).unwrap(),
            target
        );
        symlink(
            "../../usr/libexec/nanokvm/legacy/S03usbdev",
            root.join("etc/init.d/relative"),
        )
        .unwrap();
        assert_eq!(
            resolve(root, Path::new("/etc/init.d/relative"), false).unwrap(),
            target
        );
        let missing = root.join("etc/init.d/new");
        assert_eq!(resolve(root, &missing, true).unwrap(), missing);
        symlink("/missing-target", root.join("etc/init.d/dangling")).unwrap();
        assert!(resolve(root, Path::new("/etc/init.d/dangling"), false).is_err());
        assert!(resolve(root, Path::new("/../../outside"), true).is_err());
        symlink("../../../outside", root.join("etc/init.d/escape")).unwrap();
        assert!(resolve(root, Path::new("/etc/init.d/escape"), true).is_err());
        symlink("loop", root.join("etc/init.d/loop")).unwrap();
        assert!(resolve(root, Path::new("/etc/init.d/loop"), false).is_err());
    }
}
