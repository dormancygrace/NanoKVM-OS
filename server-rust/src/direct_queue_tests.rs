use super::*;
use serde_json::{json, Value};
fn frame_json(frame: &Outbound) -> Value {
    json!({"key":frame.key,"timestamp":frame.timestamp,"size":frame.payload.len()})
}
#[test]
fn complete_actual_go_direct_queue_traces_match() {
    let oracle: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/direct-queue-go-oracle.json"
    ))
    .unwrap();
    for (case_index, case) in oracle["cases"].as_array().unwrap().iter().enumerate() {
        let window = case["window"].as_u64().unwrap() as usize;
        let mut queue = Queue::new((window > 0).then_some(window));
        for (index, step) in case["steps"].as_array().unwrap().iter().enumerate() {
            let op = &step["operation"];
            let mut accepted = false;
            let mut popped = Value::Null;
            match op["Kind"].as_str().unwrap() {
                "offer" => {
                    accepted = queue.offer(Outbound {
                        key: op["Key"].as_bool().unwrap(),
                        timestamp: op["Timestamp"].as_i64().unwrap(),
                        payload: Bytes::from(vec![0; 9 + op["Size"].as_u64().unwrap() as usize]),
                    })
                }
                "pop" => popped = queue.pop().map_or(Value::Null, |frame| frame_json(&frame)),
                "ack" => {
                    queue.acknowledge(op["Timestamp"].as_i64().unwrap());
                }
                "resync" => queue.resync(),
                "discontinuity" => queue.discontinuity(),
                "close" => queue.close(),
                _ => panic!(),
            }
            let actual = json!({"operation":op,"accepted":accepted,"popped":popped,"frames":queue.frames.iter().map(frame_json).collect::<Vec<_>>(),"bytes":queue.bytes,"pending":queue.in_flight,"waiting":queue.waiting,"closed":queue.closed,"canAdvance":queue.can_advance()});
            assert_eq!(actual, *step, "case{case_index} step{index}");
        }
    }
}
#[tokio::test]
async fn closed_direct_entry_releases_native_owner_rejects_stale_delivery_and_wakes() {
    use crate::native_frame::{Budget, Pending, DIRECT_HEADROOM};
    let budget = Budget::new(8192);
    let frame = Pending::new_data(&budget, 14, DIRECT_HEADROOM)
        .unwrap()
        .finish_video(true, 7)
        .unwrap();
    let entry = Entry::new(1, None);
    entry.offer(Outbound {
        key: true,
        timestamp: 7,
        payload: frame.packet_bytes(),
    });
    drop(frame);
    assert!(budget.used() > 0);
    entry.close(Some("encoder-reconfigured"));
    assert_eq!(budget.used(), 0);
    assert!(entry.next().await.is_none());
    entry.offer(Outbound {
        key: true,
        timestamp: 8,
        payload: Bytes::from_static(b"stale"),
    });
    assert!(entry.lock().frames.is_empty());
    let entry = Entry::new(2, Some(1));
    let copy = entry.clone();
    let task = tokio::spawn(async move { copy.next().await });
    tokio::task::yield_now().await;
    entry.close(None);
    assert!(timeout(Duration::from_secs(1), task)
        .await
        .unwrap()
        .unwrap()
        .is_none());
}
