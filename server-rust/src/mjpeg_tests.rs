use super::*;
use base64::{engine::general_purpose::STANDARD, Engine};
use serde_json::{json, Value};
fn oracle() -> Value {
    serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/mjpeg-go-oracle.json"
    ))
    .unwrap()
}
fn decode(value: &Value) -> Bytes {
    Bytes::from(
        value
            .as_str()
            .map(|data| STANDARD.decode(data).unwrap())
            .unwrap_or_default(),
    )
}
fn encoded(bytes: Option<&Bytes>) -> Value {
    bytes
        .map(|bytes| json!(STANDARD.encode(bytes)))
        .unwrap_or(Value::Null)
}

#[test]
fn all596_go_jpeg_comparisons_preserve_every_byte_except_exact_app9_counter() {
    let reference = oracle();
    assert_eq!(reference["pairs"].as_array().unwrap().len(), 596);
    for (index, case) in reference["pairs"].as_array().unwrap().iter().enumerate() {
        assert_eq!(
            same_image(&decode(&case["a"]), &decode(&case["b"])),
            case["same"].as_bool().unwrap(),
            "case{index} {case}"
        );
    }
}
#[tokio::test]
async fn all17_go_delivery_transitions_match_latest_generation_refresh_cancel_and_recovery() {
    let reference = oracle();
    let clients: Vec<_> = (0..3).map(Entry::new).collect();
    let base = Instant::now();
    let mut delivery = Delivery::default();
    assert_eq!(reference["delivery"].as_array().unwrap().len(), 17);
    for (index, expected) in reference["delivery"].as_array().unwrap().iter().enumerate() {
        let operation = &expected["operation"];
        let members: Vec<_> = operation["Members"]
            .as_array()
            .into_iter()
            .flatten()
            .map(|member| clients[member.as_u64().unwrap() as usize].clone())
            .collect();
        let mut sent = false;
        let mut popped = None;
        let mut ready = false;
        match operation["Kind"].as_str().unwrap() {
            "offer" => {
                sent = delivery.offer(
                    &members,
                    decode(&operation["Data"]),
                    base + Duration::from_nanos(operation["Offset"].as_u64().unwrap()),
                );
                ready = members.iter().any(|client| client.ready());
            }
            "pop" => {
                popped = clients[operation["Client"].as_u64().unwrap() as usize]
                    .lock()
                    .frame
                    .take();
            }
            "reset" => delivery.last = None,
            "cancel" => clients[operation["Client"].as_u64().unwrap() as usize].close(),
            other => panic!("unsupported Go operation {other}"),
        }
        let actual = json!({"sent":sent,"popped":encoded(popped.as_ref()),"ok":popped.is_some(),"ready":ready,"last":encoded(delivery.last.as_ref()),"generation":delivery.generation,
            "clients":clients.iter().map(|client|{let queue=client.lock();json!({"closed":queue.closed,"pending":queue.frame.is_some(),"data":encoded(queue.frame.as_ref()),"generation":queue.generation,"offeredAt":queue.offered.map(|instant| instant.duration_since(base).as_nanos() as u64 + 1).unwrap_or(0)})}).collect::<Vec<_>>()});
        let mut expected = expected.clone();
        expected.as_object_mut().unwrap().remove("operation");
        assert_eq!(actual, expected, "step{index} {operation}");
    }
    clients[1].close();
    assert!(
        tokio::time::timeout(Duration::from_millis(30), clients[1].next())
            .await
            .unwrap()
            .is_none()
    );
}
#[test]
fn multipart_first_and_following_headers_match_actual_go_write_chunks() {
    let reference = oracle();
    assert_eq!(PART, reference["partHeader"].as_str().unwrap().as_bytes());
    assert_eq!(NEXT, decode(&reference["nextPart"]).as_ref());
    assert_eq!(
        WRITE.as_nanos() as u64,
        reference["writeTimeoutNanos"].as_u64().unwrap()
    );
    assert_eq!(
        REFRESH.as_nanos() as u64,
        reference["duplicateRefreshNanos"].as_u64().unwrap()
    );
    for (index, image) in reference["images"].as_array().unwrap().iter().enumerate() {
        let chunks = multipart(index == 0, decode(image));
        let actual: Vec<_> = chunks
            .iter()
            .flat_map(|bytes| bytes.iter().copied())
            .collect();
        assert_eq!(
            actual,
            decode(&reference["multipartChunks"][index]),
            "frame{index}"
        );
        assert!(!actual
            .windows(b"Content-Length".len())
            .any(|window| window == b"Content-Length"));
    }
}
fn frame(budget: &Arc<crate::native_frame::Budget>, data: &[u8]) -> Arc<NativeFrame> {
    let pending = crate::native_frame::Pending::new(budget, data.len()).unwrap();
    assert_eq!(
        unsafe { libc::pwrite(pending.raw_fd(), data.as_ptr().cast(), data.len(), 0) },
        data.len() as isize
    );
    pending.finish().unwrap()
}
#[tokio::test]
async fn cache_references_do_not_start_capture_and_mutable_snapshots_are_independent_budgeted_owners(
) {
    let root = tempfile::tempdir().unwrap();
    let runtime = Runtime::load(root.path()).unwrap();
    let group = &runtime.mjpeg;
    let first = group.enable_cache().unwrap();
    let second = group.enable_cache().unwrap();
    assert!(group.lock().session.is_none());
    assert_eq!(group.active.load(Ordering::Acquire), 0);
    assert!(group.latest_frame().unwrap().is_none());
    assert!(group.add().await.is_err());
    let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
    let budget = crate::native_frame::Budget::new(3 * page);
    let native = frame(&budget, b"cache-jpeg");
    let screen = screen::Screen {
        width: 800,
        height: 600,
        ..Default::default()
    };
    group.cache_frame(native.clone(), screen);
    drop(native);
    assert_eq!(budget.used(), page);
    let mut copy = group.latest_frame().unwrap().unwrap();
    assert_eq!((copy.width, copy.height), (800, 600));
    assert_eq!(copy.data.as_ref(), b"cache-jpeg");
    copy.data[0] = b'X';
    let next = group.latest_frame().unwrap().unwrap();
    assert_eq!(next.data.as_ref(), b"cache-jpeg");
    assert_eq!(copy.captured_at, next.captured_at);
    assert_eq!(budget.used(), 3 * page);
    assert!(group.latest_frame().is_err());
    drop(first);
    assert_eq!(group.cache.lock().unwrap().refs, 1);
    drop(second);
    assert!(group.cache.lock().unwrap().latest.is_none());
    assert!(group.latest_frame().unwrap().is_none());
    assert_eq!(budget.used(), 2 * page);
    drop(copy);
    drop(next);
    assert_eq!(budget.used(), 0);
    runtime.shutdown_async().await.unwrap();
}
#[tokio::test]
async fn closing_full_latest_slot_wakes_reader_and_releases_native_owner_after_delivery_reset() {
    let page = unsafe { libc::sysconf(libc::_SC_PAGESIZE) } as usize;
    let budget = crate::native_frame::Budget::new(page);
    let native = frame(&budget, b"native-jpeg");
    let client = Entry::new(1);
    let mut delivery = Delivery::default();
    assert!(delivery.offer(
        std::slice::from_ref(&client),
        native.data_bytes(),
        Instant::now()
    ));
    drop(native);
    assert_eq!(budget.used(), page);
    client.close();
    assert!(!client.ready());
    assert!(client.next().await.is_none());
    assert_eq!(budget.used(), page);
    delivery.last = None;
    assert_eq!(budget.used(), 0);
}
