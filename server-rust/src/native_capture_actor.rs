//! Bounded asynchronous admission to the single blocking capture owner.
use crate::{
    native_capture::{Outcome, Request, Worker},
    native_protocol, Error,
};
use std::{
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Mutex,
    },
    thread::JoinHandle,
    time::{Duration, Instant},
};
use tokio::sync::{mpsc, oneshot};

const QUEUE_CAPACITY: usize = 8;
struct Job {
    request: Request,
    origin: Instant,
    deadline: Instant,
    cancelled: Arc<AtomicBool>,
    reply: oneshot::Sender<Result<Outcome, Error>>,
}
enum Message {
    Call(Job),
    Stop,
}
struct Owner {
    sender: mpsc::Sender<Message>,
    stopping: Arc<AtomicBool>,
    finished: Arc<AtomicBool>,
    thread: Mutex<Option<JoinHandle<()>>>,
}
impl Owner {
    fn stop(&self) {
        self.stopping.store(true, Ordering::Release);
        let _ = self.sender.try_send(Message::Stop);
    }
    fn join(&self) -> Result<(), Error> {
        // Hold the join lock until exit: concurrent shutdown callers must
        // not return early after another caller takes the JoinHandle.
        let mut holder = self
            .thread
            .lock()
            .map_err(|_| "native owner join lock poisoned")?;
        if let Some(thread) = holder.take() {
            thread.join().map_err(|_| "native capture owner panicked")?;
        }
        Ok(())
    }
}
impl Drop for Owner {
    fn drop(&mut self) {
        self.stop();
        let _ = self.join();
    }
}
#[derive(Clone)]
pub struct Actor {
    owner: Arc<Owner>,
}
struct CancelOnDrop(Arc<AtomicBool>);
impl Drop for CancelOnDrop {
    fn drop(&mut self) {
        self.0.store(true, Ordering::Release);
    }
}
impl Actor {
    /// Takes exclusive ownership of an explicitly launched native worker.
    /// A fixed thread prevents native waits from blocking the Tokio event loop.
    pub fn new(mut worker: Worker) -> Result<Self, Error> {
        let (sender, mut receiver) = mpsc::channel(QUEUE_CAPACITY);
        let stopping = Arc::new(AtomicBool::new(false));
        let stop = stopping.clone();
        let finished = Arc::new(AtomicBool::new(false));
        let done = finished.clone();
        let thread = std::thread::Builder::new()
            .name("native-capture".into())
            .spawn(move || {
                while !stop.load(Ordering::Acquire) {
                    let Some(message) = receiver.blocking_recv() else {
                        break;
                    };
                    let Message::Call(job) = message else { break };
                    if stop.load(Ordering::Acquire)
                        || job.cancelled.load(Ordering::Acquire)
                        || job.reply.is_closed()
                    {
                        let _ = job
                            .reply
                            .send(Err("native capture cancelled before admission".into()));
                        continue;
                    }
                    let remaining = job.deadline.saturating_duration_since(Instant::now());
                    if remaining.is_zero() {
                        let _ = job
                            .reply
                            .send(Err("native capture queue deadline expired".into()));
                        continue;
                    }
                    let result = worker.call(job.request, job.origin, remaining, &|| {
                        stop.load(Ordering::Acquire)
                            || job.cancelled.load(Ordering::Acquire)
                            || job.reply.is_closed()
                    });
                    let _ = job.reply.send(result);
                    if worker.closed() {
                        break;
                    }
                }
                stop.store(true, Ordering::Release);
                receiver.close();
                while let Ok(message) = receiver.try_recv() {
                    if let Message::Call(job) = message {
                        let _ = job.reply.send(Err("native capture owner stopped".into()));
                    }
                }
                // Normal teardown requests native deinit; poisoned IPC is already
                // terminated. No automatic relaunch after an uncertain native failure.
                drop(worker);
                done.store(true, Ordering::Release);
            })?;
        Ok(Self {
            owner: Arc::new(Owner {
                sender,
                stopping,
                finished,
                thread: Mutex::new(Some(thread)),
            }),
        })
    }
    pub async fn call(
        &self,
        request: Request,
        origin: Instant,
        timeout: Duration,
    ) -> Result<Outcome, Error> {
        if self.owner.stopping.load(Ordering::Acquire) {
            return Err("native capture owner stopped".into());
        }
        let deadline = native_protocol::deadline(timeout)?;
        let cancelled = Arc::new(AtomicBool::new(false));
        let _guard = CancelOnDrop(cancelled.clone());
        let (reply, receiver) = oneshot::channel();
        let job = Job {
            request,
            origin,
            deadline,
            cancelled,
            reply,
        };
        self.owner
            .sender
            .try_send(Message::Call(job))
            .map_err(|error| match error {
                mpsc::error::TrySendError::Full(_) => "native capture queue is full",
                mpsc::error::TrySendError::Closed(_) => "native capture owner stopped",
            })?;
        tokio::time::timeout_at(tokio::time::Instant::from_std(deadline), receiver)
            .await
            .map_err(|_| "native capture request timed out")?
            .map_err(|_| "native capture owner stopped")?
    }
    pub fn stop(&self) {
        self.owner.stop();
    }
    pub fn finished(&self) -> bool {
        self.owner.finished.load(Ordering::Acquire)
    }
    /// Call during async shutdown before dropping the last owner handle.
    pub async fn shutdown(&self) -> Result<(), Error> {
        self.stop();
        let owner = self.owner.clone();
        tokio::task::spawn_blocking(move || owner.join()).await?
    }
}
