use super::*;
use crate::video_source::tests::Fixture;
use std::fs;
const JPEG: &[u8] = include_bytes!("../tests/fixtures/mjpeg-red-app9.jpg");
fn captures(fixture: &Fixture) -> usize {
    fixture
        .trace()
        .lines()
        .filter(|line| line.starts_with("mjpeg:"))
        .count()
}
async fn next(viewer: &Viewer) -> Bytes {
    tokio::time::timeout(Duration::from_secs(3), viewer.entry.next())
        .await
        .unwrap()
        .unwrap()
}
async fn captured_after(fixture: &Fixture, before: usize) {
    tokio::time::timeout(Duration::from_secs(3), async {
        while captures(fixture) <= before {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
}
#[tokio::test]
async fn actual_c_shared_static_app9_new_viewer_and_skip_recovery_deliver_without_duplicate_capture_fps(
) {
    let fixture = Fixture::new_mjpeg(JPEG);
    fixture.initialize().await;
    let group = &fixture.runtime.mjpeg;
    let first = group.add().await.unwrap();
    let second = group.add().await.unwrap();
    let a = next(&first).await;
    let b = next(&second).await;
    assert_eq!(a, b);
    assert!(same_image(&a, JPEG));
    assert!(captures(&fixture) >= 2);
    drop(a);
    drop(b);
    assert!(
        tokio::time::timeout(Duration::from_millis(100), first.entry.next())
            .await
            .is_err()
    );
    let third = group.add().await.unwrap();
    let c = next(&third).await;
    assert!(same_image(&c, JPEG));
    drop(c);
    assert!(
        tokio::time::timeout(Duration::from_millis(100), first.entry.next())
            .await
            .is_err()
    );
    let before = captures(&fixture);
    fs::write(fixture.root.path().join("mjpeg-status"), "5").unwrap();
    captured_after(&fixture, before + 2).await;
    assert!(first.entry.lock().frame.is_none());
    fs::write(fixture.root.path().join("mjpeg-status"), "0").unwrap();
    let recovered = next(&first).await;
    assert!(same_image(&recovered, JPEG));
    drop(recovered);
    assert!(!fixture.backend.actor().stopped());
    drop(first);
    drop(second);
    drop(third);
    group.join().await;
    assert!(group.lock().session.is_none());
    assert_eq!(group.active.load(Ordering::Acquire), 0);
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
}
#[tokio::test]
async fn actual_c_last_mjpeg_close_drains_held_read_before_replacement_without_poisoning_shared_actor(
) {
    let fixture = Fixture::new_mjpeg(JPEG);
    fixture.initialize().await;
    let group = fixture.runtime.mjpeg.clone();
    let viewer = group.add().await.unwrap();
    drop(next(&viewer).await);
    let before = captures(&fixture);
    let hold = fixture.root.path().join("mjpeg-hold");
    fs::write(&hold, "hold").unwrap();
    captured_after(&fixture, before).await;
    fixture.until_trace("jpeg-hold:").await;
    drop(viewer);
    let copy = group.clone();
    let replacement = tokio::spawn(async move { copy.add().await });
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert!(!replacement.is_finished());
    assert!(!fixture.backend.actor().stopped());
    fs::remove_file(hold).unwrap();
    let replacement = tokio::time::timeout(Duration::from_secs(3), replacement)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(same_image(&next(&replacement).await, JPEG));
    drop(replacement);
    group.join().await;
    assert!(!fixture.backend.actor().stopped());
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
}
