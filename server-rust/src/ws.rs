//! Authenticated input socket slice. Capture snapshots and addon services
//! remain migration gates; this module does not claim complete WS parity.
use crate::{
    api,
    hid_reports::{self, Frame, Report},
    input::{ClientId, Ticket},
    inputcontrol::{ManualSession, Reservation},
    sessions::Principal,
    Runtime,
};
use axum::{
    extract::{
        ws::{
            rejection::WebSocketUpgradeRejection, CloseFrame, Message, WebSocket, WebSocketUpgrade,
        },
        ConnectInfo, State,
    },
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    Json,
};
use futures_util::{stream::SplitSink, SinkExt, StreamExt};
use serde_json::json;
use std::{net::SocketAddr, path::PathBuf, sync::Arc, time::Duration};
use tokio::{
    sync::{mpsc, watch, OwnedSemaphorePermit},
    task::JoinHandle,
    time::{timeout, Instant},
};

const HEARTBEAT: Duration = Duration::from_secs(90);
const WRITE: Duration = Duration::from_secs(10);

pub async fn connect(
    State((runtime, _)): State<(Arc<Runtime>, PathBuf)>,
    ConnectInfo(peer): ConnectInfo<SocketAddr>,
    headers: HeaderMap,
    upgrade: Result<WebSocketUpgrade, WebSocketUpgradeRejection>,
) -> Response {
    crate::jiggler::Jiggler::start(&runtime);
    let Ok(permit) = runtime.jobs.clone().try_acquire_owned() else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    let copy = runtime.clone();
    let auth_headers = headers.clone();
    let principal = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        api::principal(&copy, &auth_headers)
    })
    .await;
    let Ok(Ok(principal)) = principal else {
        return api::unauthorized();
    };
    if principal.user.must_change_password {
        return (
            StatusCode::FORBIDDEN,
            Json(json!({"code":-10,"msg":"password change required"})),
        )
            .into_response();
    }
    if !crate::ws_origin::allowed(&runtime.config, &headers, peer.ip()) {
        return (
            StatusCode::FORBIDDEN,
            "websocket: request origin not allowed",
        )
            .into_response();
    }
    let upgrade = match upgrade {
        Ok(value) => value,
        Err(error) => return error.into_response(),
    };
    let Ok(slot) = runtime.socket_slots.clone().try_acquire_owned() else {
        return StatusCode::SERVICE_UNAVAILABLE.into_response();
    };
    upgrade
        .max_message_size(4 << 10)
        .max_frame_size(4 << 10)
        .on_upgrade(move |socket| run(socket, runtime, principal, slot))
}
async fn cancelled(receiver: &mut watch::Receiver<bool>) {
    loop {
        if *receiver.borrow_and_update() {
            return;
        }
        if receiver.changed().await.is_err() {
            return;
        }
    }
}
async fn send(
    sink: &mut SplitSink<WebSocket, Message>,
    message: Message,
    revoked: &mut watch::Receiver<bool>,
) -> bool {
    tokio::select! {
        biased;
        _ = cancelled(revoked) => false,
        result = timeout(WRITE, sink.send(message)) => matches!(result, Ok(Ok(()))),
    }
}
fn worker(
    runtime: Arc<Runtime>,
    principal: Principal,
    mut queue: mpsc::Receiver<(Ticket, Report, Reservation)>,
) -> JoinHandle<()> {
    tokio::spawn(async move {
        while let Some((ticket, report, reservation)) = queue.recv().await {
            let Ok(permit) = runtime.hid_jobs.clone().acquire_owned().await else {
                break;
            };
            let copy = runtime.clone();
            let who = principal.clone();
            let result = tokio::task::spawn_blocking(move || {
                let _permit = permit;
                copy.input.execute(ticket, || {
                    if !who.valid(&copy) {
                        return Err("session expired or revoked".into());
                    }
                    reservation
                        .execute(|| {
                            let result = copy.hid.write(&report);
                            if result.is_err() {
                                // Finish cleanup before returning the manual lane to addons.
                                if let Err(error) = copy.hid.release_all() {
                                    eprintln!("HID failure cleanup failed: {error}");
                                }
                            }
                            result
                        })
                        .map(|_| ())
                })
            })
            .await;
            if let Ok(Err(error)) = result {
                eprintln!("HID report failed: {error}");
            }
        }
    })
}
async fn leave(runtime: &Arc<Runtime>, id: ClientId) {
    // Keep teardown independent of long API work. Retain registry membership
    // until admitted so shutdown can still revoke sessions queued for cleanup.
    let Ok(permit) = runtime.cleanup_jobs.clone().acquire_owned().await else {
        return;
    };
    let copy = runtime.clone();
    let _ = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        copy.sessions.remove(id);
        copy.input.leave(id)
    })
    .await;
}
async fn run(
    socket: WebSocket,
    runtime: Arc<Runtime>,
    principal: Principal,
    _slot: OwnedSemaphorePermit,
) {
    let copy = runtime.clone();
    let Ok(permit) = runtime.cleanup_jobs.clone().acquire_owned().await else {
        return;
    };
    let registration = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        if let Err(error) = copy.hid.open() {
            eprintln!("HID open failed: {error}");
        }
        let manual = ManualSession::new(copy.control.clone(), copy.coordinator.clone());
        let cleanup = manual.clone();
        let hid = copy.hid.clone();
        let (id, status) = copy.input.join_with_cleanup(move |closed| {
            if let Err(error) = cleanup.revoke(closed, || hid.release_all()) {
                eprintln!("manual session cleanup failed: {error}");
            }
        })?;
        Ok::<_, crate::Error>((id, status, manual))
    })
    .await;
    let Ok(Ok((id, mut status, manual))) = registration else {
        return;
    };
    let mut revoked = match runtime.sessions.register(id, &principal.user.username) {
        Ok(receiver) => receiver,
        Err(_) => {
            leave(&runtime, id).await;
            return;
        }
    };
    let copy = runtime.clone();
    let who = principal.clone();
    let Ok(permit) = runtime.cleanup_jobs.clone().acquire_owned().await else {
        leave(&runtime, id).await;
        return;
    };
    let valid = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        who.valid(&copy)
    })
    .await
    .unwrap_or(false);
    let (mut sink, mut stream) = socket.split();
    if !valid {
        let _ = timeout(
            Duration::from_secs(2),
            sink.send(Message::Close(Some(CloseFrame {
                code: 4401,
                reason: "session expired or revoked".into(),
            }))),
        )
        .await;
        leave(&runtime, id).await;
        return;
    }
    let (keyboard, keyboard_queue) = mpsc::channel(200);
    let (mouse, mouse_queue) = mpsc::channel(200);
    let workers = [
        worker(runtime.clone(), principal.clone(), keyboard_queue),
        worker(runtime.clone(), principal.clone(), mouse_queue),
    ];
    let leds = runtime.hid.leds();
    let mut led_updates = leds.subscribe();
    let led_snapshot = serde_json::to_string(&leds.snapshot(false)).unwrap();
    let sent_leds = send(
        &mut sink,
        Message::Text(
            json!({"type":"hid-led-status","data":led_snapshot})
                .to_string()
                .into(),
        ),
        &mut revoked,
    )
    .await;
    let initial = serde_json::to_string(&*status.borrow_and_update()).expect("control status");
    let mut running = sent_leds
        && send(
            &mut sink,
            Message::Text(json!({"type":"control","data":initial}).to_string().into()),
            &mut revoked,
        )
        .await;
    let mut capture_updates = runtime.capture_status.subscribe();
    let mut capture_sent = capture_updates.borrow_and_update().clone();
    for capture in capture_sent.values() {
        let data = serde_json::to_string(capture).expect("capture status");
        if !send(
            &mut sink,
            Message::Text(
                json!({"type":"capture-status","data":data})
                    .to_string()
                    .into(),
            ),
            &mut revoked,
        )
        .await
        {
            running = false;
            break;
        }
    }
    let mut heartbeat = Instant::now() + HEARTBEAT;
    let mut check = tokio::time::interval(Duration::from_secs(1));
    check.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    // Keep timer arithmetic bounded even with a very distant signed expiry.
    // The periodic session check still observes revocation/clock changes.
    let expiry = tokio::time::sleep(expiry_delay(principal.expires, api::now()));
    tokio::pin!(expiry);
    let mut session_revoked = false;
    let mut failure_close = None;
    while running {
        tokio::select! {
            biased;
            _ = cancelled(&mut revoked) => { session_revoked = true; break; }
            _ = &mut expiry => {
                let remaining=expiry_delay(principal.expires,api::now());
                if remaining.is_zero(){session_revoked=true;break;}
                expiry.as_mut().reset(Instant::now()+remaining);
            }
            _ = tokio::time::sleep_until(heartbeat) => break,
            _ = check.tick() => {
                // Expiry/clock failure cannot depend on free blocking API jobs.
                if expiry_delay(principal.expires,api::now()).is_zero(){session_revoked=true;break;}
                let Ok(permit) = runtime.jobs.clone().try_acquire_owned() else { continue; };
                let copy = runtime.clone(); let who = principal.clone();
                let valid = tokio::task::spawn_blocking(move || { let _permit = permit; who.valid(&copy) }).await.unwrap_or(false);
                if !valid { session_revoked = true; break; }
            }
            changed = status.changed() => {
                if changed.is_err() { break; }
                let data = serde_json::to_string(&*status.borrow_and_update()).expect("control status");
                running = send(&mut sink, Message::Text(json!({"type":"control","data":data}).to_string().into()), &mut revoked).await;
            }
            changed = capture_updates.changed() => {
                if changed.is_err() { break; }
                let captures = capture_updates.borrow_and_update().clone();
                for (mode,capture) in captures.iter() {
                    if capture_sent.get(mode) == Some(capture) { continue; }
                    let data = serde_json::to_string(capture).expect("capture status");
                    if !send(&mut sink,Message::Text(json!({"type":"capture-status","data":data}).to_string().into()),&mut revoked).await {running=false;break;}
                }
                capture_sent = captures;
            }
            changed = led_updates.changed() => {
                if changed.is_err() { break; }
                let data = serde_json::to_string(&leds.snapshot(false)).unwrap();
                running = send(&mut sink, Message::Text(json!({"type":"hid-led-status","data":data}).to_string().into()), &mut revoked).await;
            }
            event = stream.next() => {
                let message = match event {
                    Some(Ok(message)) => message,
                    Some(Err(error)) => {
                        if error.into_inner().downcast_ref::<tokio_tungstenite::tungstenite::Error>().is_some_and(|error| matches!(error, tokio_tungstenite::tungstenite::Error::Capacity(_))) {
                            failure_close = Some(CloseFrame { code: 1009, reason: "message too big".into() });
                        }
                        break;
                    }
                    None => break,
                };
                heartbeat = Instant::now() + HEARTBEAT;
                let frame = match &message {
                    Message::Binary(bytes) => hid_reports::parse(bytes),
                    Message::Text(text) => hid_reports::parse(text.as_bytes()),
                    Message::Close(_) => break,
                    Message::Ping(bytes) => { running = send(&mut sink, Message::Pong(bytes.clone()), &mut revoked).await; continue; }
                    _ => continue,
                };
                match frame {
                    Some(Frame::Heartbeat) => running = send(&mut sink, Message::Text(json!({"type":"heartbeat","data":""}).to_string().into()), &mut revoked).await,
                    Some(Frame::Control(enabled)) => {
                        let copy=runtime.clone();let who=principal.clone();
                        let permit=tokio::select! {
                            biased;
                            _=cancelled(&mut revoked)=>{session_revoked=true;break;}
                            result=timeout(Duration::from_secs(2),runtime.cleanup_jobs.clone().acquire_owned())=>match result {Ok(Ok(permit))=>permit,_=>break}
                        };
                        // Control/release cannot be stalled behind long API work.
                        running=matches!(tokio::task::spawn_blocking(move||{
                            let _permit=permit;
                            if !who.valid(&copy){return Err("session expired or revoked".into());}
                            copy.input.set_control(id,enabled)
                        }).await,Ok(Ok(())));
                    }
                    Some(Frame::Report(report)) => {
                        let Some(ticket) = runtime.input.ticket(id) else { continue; };
                        // Admission has its own bounded lane: waiting for addon cleanup
                        // must not occupy HID workers or block unrelated HTTP requests.
                        let permit = tokio::select! {
                            biased;
                            _ = cancelled(&mut revoked) => { session_revoked = true; break; }
                            result = timeout(Duration::from_secs(2), runtime.control_jobs.clone().acquire_owned()) => {
                                match result { Ok(Ok(permit)) => permit, _ => continue }
                            }
                        };
                        let session = manual.clone();
                        let copy = runtime.clone();
                        let (kind, held, cooldown) = (report.kind(), report.held(), report.starts_cooldown());
                        let reservation = tokio::select! {
                            biased;
                            _ = cancelled(&mut revoked) => { session_revoked = true; break; }
                            result = tokio::task::spawn_blocking(move || {
                                let _permit = permit;
                                session.reserve(kind, held, cooldown, Duration::from_secs(2), |mode| {
                                    mode != crate::controlmode::Mode::Picoclaw || copy.pico_lock.owner().is_empty()
                                })
                            }) => { match result { Ok(Ok(reservation)) => reservation, _ => continue } }
                        };
                        // Ownership may have moved while this socket waited for a reservation.
                        if runtime.input.ticket(id).is_none() { drop(reservation); continue; }
                        let queue = if report.kind() == hid_reports::Kind::Keyboard { &keyboard } else { &mouse };
                        tokio::select! {
                            biased;
                            _ = cancelled(&mut revoked) => { session_revoked = true; break; }
                            // Bounded backpressure, without retaining an unlimited list of reports.
                            result = timeout(Duration::from_secs(2), queue.send((ticket, report, reservation))) => {
                                if matches!(result, Ok(Ok(()))) { runtime.jiggler.update(); }
                            },
                        }
                    }
                    None => {},
                }
            }
        }
    }
    session_revoked |= *revoked.borrow() || expiry_delay(principal.expires, api::now()).is_zero();
    for worker in workers {
        worker.abort();
    }
    leave(&runtime, id).await;
    if session_revoked {
        let _ = timeout(
            Duration::from_secs(2),
            sink.send(Message::Close(Some(CloseFrame {
                code: 4401,
                reason: "session expired or revoked".into(),
            }))),
        )
        .await;
    } else {
        let _ = timeout(
            Duration::from_secs(2),
            sink.send(Message::Close(failure_close)),
        )
        .await;
    }
}

fn expiry_delay(expires: Option<u64>, now: Result<u64, crate::Error>) -> Duration {
    Duration::from_secs(match expires {
        None => 3600,
        Some(exp) => now
            .map(|now| exp.saturating_sub(now).min(3600))
            .unwrap_or(0),
    })
}
#[cfg(test)]
mod clock_tests {
    use super::*;
    #[test]
    fn expiry_timer_is_bounded_and_invalid_clock_closes_authenticated_sessions() {
        assert_eq!(
            expiry_delay(Some(u64::MAX), Ok(0)),
            Duration::from_secs(3600)
        );
        assert_eq!(expiry_delay(Some(120), Ok(100)), Duration::from_secs(20));
        assert_eq!(expiry_delay(Some(100), Ok(100)), Duration::ZERO);
        assert_eq!(
            expiry_delay(Some(100), Err("clock unavailable".into())),
            Duration::ZERO
        );
        assert_eq!(
            expiry_delay(None, Err("clock unavailable".into())),
            Duration::from_secs(3600)
        );
    }
}
