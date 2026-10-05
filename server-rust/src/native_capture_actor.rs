//! One bounded queue for async captures, blocking API controls and compound
//! native maintenance. The owner thread runs every job to its cleanup boundary.
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
type Work = Box<dyn FnOnce(&mut Worker, &Context) -> Result<Outcome, Error> + Send>;
pub(crate) struct Context {
    origin: Instant,
    deadline: Instant,
    cancelled: Arc<AtomicBool>,
    stopping: Arc<AtomicBool>,
}
impl Context {
    pub(crate) fn cancelled(&self) -> bool {
        self.cancelled.load(Ordering::Acquire) || self.stopping.load(Ordering::Acquire)
    }
    pub(crate) fn remaining(&self) -> Result<Duration, Error> {
        if self.cancelled() {
            return Err("native capture cancelled".into());
        }
        let remaining = self.deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            Err("native capture queue deadline expired".into())
        } else {
            Ok(remaining)
        }
    }
    pub(crate) fn call(&self, worker: &mut Worker, request: Request) -> Result<Outcome, Error> {
        worker.call(request, self.origin, self.remaining()?, &|| {
            self.cancelled()
        })
    }
}
struct Job {
    work: Work,
    context: Context,
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
struct Admission {
    receiver: oneshot::Receiver<Result<Outcome, Error>>,
    guard: CancelOnDrop,
    deadline: Instant,
}
impl Actor {
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
                    let Message::Call(job) = message else {
                        break;
                    };
                    let result = if job.reply.is_closed() {
                        Err("native capture cancelled before admission".into())
                    } else {
                        job.context
                            .remaining()
                            .and_then(|_| (job.work)(&mut worker, &job.context))
                    };
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
    fn admit(&self, work: Work, origin: Instant, timeout: Duration) -> Result<Admission, Error> {
        if self.owner.stopping.load(Ordering::Acquire) {
            return Err("native capture owner stopped".into());
        }
        let deadline = native_protocol::deadline(timeout)?;
        let cancelled = Arc::new(AtomicBool::new(false));
        let guard = CancelOnDrop(cancelled.clone());
        let (reply, receiver) = oneshot::channel();
        let context = Context {
            origin,
            deadline,
            cancelled,
            stopping: self.owner.stopping.clone(),
        };
        self.owner
            .sender
            .try_send(Message::Call(Job {
                work,
                context,
                reply,
            }))
            .map_err(|error| match error {
                mpsc::error::TrySendError::Full(_) => "native capture queue is full",
                mpsc::error::TrySendError::Closed(_) => "native capture owner stopped",
            })?;
        Ok(Admission {
            receiver,
            guard,
            deadline,
        })
    }
    pub async fn call(
        &self,
        request: Request,
        origin: Instant,
        timeout: Duration,
    ) -> Result<Outcome, Error> {
        let admission = self.admit(
            Box::new(move |worker, context| context.call(worker, request)),
            origin,
            timeout,
        )?;
        let _guard = admission.guard;
        tokio::time::timeout_at(
            tokio::time::Instant::from_std(admission.deadline),
            admission.receiver,
        )
        .await
        .map_err(|_| "native capture request timed out")?
        .map_err(|_| "native capture owner stopped")?
    }
    /// Only for an API blocking worker or a dedicated thread. Never wait on a
    /// Tokio event-loop thread. Admission shares the same fixed eight slots.
    pub fn call_blocking(
        &self,
        request: Request,
        origin: Instant,
        timeout: Duration,
        cancelled: &dyn Fn() -> bool,
    ) -> Result<Outcome, Error> {
        self.transaction_blocking(
            origin,
            timeout,
            Duration::ZERO,
            cancelled,
            move |worker, context| context.call(worker, request),
        )
    }
    /// Trusted bounded work only. Compound maintenance keeps the owner until
    /// its finally/restore step, so no capture/control interleaves with it.
    pub(crate) fn transaction_blocking<F>(
        &self,
        origin: Instant,
        timeout: Duration,
        cleanup_grace: Duration,
        cancelled: &dyn Fn() -> bool,
        work: F,
    ) -> Result<Outcome, Error>
    where
        F: FnOnce(&mut Worker, &Context) -> Result<Outcome, Error> + Send + 'static,
    {
        if cancelled() {
            return Err("native capture cancelled before admission".into());
        }
        let mut admission = self.admit(Box::new(work), origin, timeout)?;
        let mut cancelled_at = None;
        loop {
            match admission.receiver.try_recv() {
                Ok(result) => return result,
                Err(oneshot::error::TryRecvError::Closed) => {
                    return Err("native capture owner stopped".into())
                }
                Err(oneshot::error::TryRecvError::Empty) => {}
            }
            let now = Instant::now();
            if cancelled()
                || now >= admission.deadline
                || self.owner.stopping.load(Ordering::Acquire)
            {
                admission.guard.0.store(true, Ordering::Release);
                let since = *cancelled_at.get_or_insert(now);
                if now.saturating_duration_since(since) >= cleanup_grace {
                    return Err("native capture request cancelled or timed out".into());
                }
            }
            std::thread::sleep(Duration::from_millis(5));
        }
    }
    pub fn stop(&self) {
        self.owner.stop();
    }
    pub fn finished(&self) -> bool {
        self.owner.finished.load(Ordering::Acquire)
    }
    pub fn join_blocking(&self) -> Result<(), Error> {
        self.stop();
        self.owner.join()
    }
    pub async fn shutdown(&self) -> Result<(), Error> {
        self.stop();
        let owner = self.owner.clone();
        tokio::task::spawn_blocking(move || owner.join()).await?
    }
}
