//! One lazy GPIO input worker. No runtime reference cycle; stop-before-start works.
use crate::{
    gpio::{joined, Backend, Line},
    hardware::Hardware,
    Error,
};
use serde::Serialize;
use std::{
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc, Condvar, Mutex,
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant},
};
#[derive(Default, Clone, Copy, Debug, PartialEq, Eq, Serialize)]
pub struct Status {
    #[serde(rename = "pwr")]
    pub power: bool,
    pub hdd: bool,
}
#[derive(Default)]
struct Sample {
    ready: bool,
    status: Status,
    error: Option<String>,
}
struct Inner {
    backend: Arc<dyn Backend>,
    power: String,
    hdd: String,
    sample: Mutex<Sample>,
    wake: Condvar,
    stop: AtomicBool,
}
impl Inner {
    fn publish(&self, status: Status, error: Option<String>) {
        let mut sample = self.sample.lock().unwrap_or_else(|e| e.into_inner());
        sample.status = status;
        sample.error = error;
        sample.ready = true;
        self.wake.notify_all();
    }
    fn wait(&self, duration: Duration) -> bool {
        let sample = self.sample.lock().unwrap_or_else(|e| e.into_inner());
        let _sample = self
            .wake
            .wait_timeout_while(sample, duration, |_| !self.stop.load(Ordering::Acquire))
            .unwrap_or_else(|e| e.into_inner());
        !self.stop.load(Ordering::Acquire)
    }
    fn run(&self) {
        while !self.stop.load(Ordering::Acquire) {
            let mut power = match self.backend.open(&self.power, false) {
                Ok(line) => line,
                Err(error) => {
                    self.publish(Status::default(), Some(error.to_string()));
                    if !self.wait(Duration::from_secs(1)) {
                        return;
                    }
                    continue;
                }
            };
            let mut hdd = if self.hdd.is_empty() {
                None
            } else {
                match self.backend.open(&self.hdd, false) {
                    Ok(line) => Some(line),
                    Err(error) => {
                        let error = joined(Err(error), [power.close()]).unwrap_err();
                        self.publish(Status::default(), Some(error.to_string()));
                        if !self.wait(Duration::from_secs(1)) {
                            return;
                        }
                        continue;
                    }
                }
            };
            let result = self.sample_inputs(&mut *power, &mut hdd);
            let result = joined(
                result,
                [
                    power.close(),
                    hdd.as_mut().map_or(Ok(()), |line| line.close()),
                ],
            );
            if let Err(error) = result {
                self.publish(Status::default(), Some(error.to_string()));
            }
            if !self.wait(Duration::from_secs(1)) {
                return;
            }
        }
    }
    fn sample_inputs(
        &self,
        power: &mut dyn Line,
        hdd: &mut Option<Box<dyn Line>>,
    ) -> Result<(), Error> {
        let mut until = None;
        loop {
            if self.stop.load(Ordering::Acquire) {
                return Ok(());
            }
            let power_high = power.get()?;
            let now = Instant::now();
            let mut active = false;
            if let Some(line) = hdd {
                let high = line.get()?;
                if !high {
                    until = Some(now + Duration::from_millis(350));
                }
                active = !high || until.is_some_and(|end| now < end);
            }
            self.publish(
                Status {
                    power: !power_high,
                    hdd: active,
                },
                None,
            );
            if !self.wait(Duration::from_millis(20)) {
                return Ok(());
            }
        }
    }
}
pub struct Monitor {
    inner: Arc<Inner>,
    worker: Mutex<Option<JoinHandle<()>>>,
}
impl Monitor {
    pub fn new(hardware: &Hardware, backend: Arc<dyn Backend>) -> Self {
        Self {
            inner: Arc::new(Inner {
                backend,
                power: hardware.power_led.clone(),
                hdd: hardware.hdd_led.clone(),
                sample: Mutex::new(Sample::default()),
                wake: Condvar::new(),
                stop: AtomicBool::new(false),
            }),
            worker: Mutex::new(None),
        }
    }
    pub fn current(&self, cancelled: impl Fn() -> bool) -> Result<Status, Error> {
        {
            let mut worker = self.worker.lock().map_err(|_| "GPIO monitor unavailable")?;
            if self.inner.stop.load(Ordering::Acquire) {
                return Err("GPIO monitor stopped".into());
            }
            if worker.is_none() {
                let inner = self.inner.clone();
                *worker = Some(
                    thread::Builder::new()
                        .name("nanokvm-atx-leds".into())
                        .spawn(move || inner.run())?,
                );
            }
        }
        let mut sample = self
            .inner
            .sample
            .lock()
            .map_err(|_| "GPIO monitor unavailable")?;
        loop {
            if cancelled() {
                return Err("context canceled".into());
            }
            if self.inner.stop.load(Ordering::Acquire) {
                return Err("GPIO monitor stopped".into());
            }
            if sample.ready {
                return match &sample.error {
                    Some(error) => Err(error.clone().into()),
                    None => Ok(sample.status),
                };
            }
            sample = self
                .inner
                .wake
                .wait_timeout(sample, Duration::from_millis(20))
                .map_err(|_| "GPIO monitor unavailable")?
                .0;
        }
    }
    pub fn stop(&self) {
        let mut worker = self.worker.lock().unwrap_or_else(|e| e.into_inner());
        {
            let _sample = self.inner.sample.lock().unwrap_or_else(|e| e.into_inner());
            self.inner.stop.store(true, Ordering::Release);
            self.inner.wake.notify_all();
        }
        if let Some(worker) = worker.take() {
            if worker.join().is_err() {
                eprintln!("GPIO monitor worker panicked");
            }
        }
    }
}
impl Drop for Monitor {
    fn drop(&mut self) {
        self.stop();
    }
}
