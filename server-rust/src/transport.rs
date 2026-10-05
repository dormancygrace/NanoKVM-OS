//! Per-response write deadlines, including flow-blocked HTTP/2 streams.
use axum::{
    body::{Body, Bytes},
    http::{header, HeaderMap, HeaderValue, Method, Request, Response},
    response::Response as AxumResponse,
};
use bytes::Buf;
use futures_util::{future::poll_fn, task::AtomicWaker};
use http_body::{Body as HttpBody, Frame, SizeHint};
use std::{
    convert::Infallible,
    future::Future,
    io,
    pin::Pin,
    sync::{Arc, Mutex, Weak},
    task::{Context, Poll},
    time::{Duration, SystemTime},
};
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWrite, ReadBuf},
    net::TcpStream,
    sync::{watch, Notify},
    task::JoinSet,
    time::{Instant, Sleep},
};
use tower::{Service, ServiceExt};

const H2_PREFACE: &[u8] = b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n";
const DATA_CHUNK: usize = 16 * 1024;
const MAX_STREAMS: u32 = 128;

#[derive(Clone)]
pub struct Shutdown(watch::Sender<bool>);
impl Default for Shutdown {
    fn default() -> Self {
        Self(watch::channel(false).0)
    }
}
impl Shutdown {
    pub fn stop(&self) {
        self.0.send_replace(true);
    }
    pub async fn stopped(&self) {
        let mut rx = self.0.subscribe();
        while !*rx.borrow_and_update() {
            if rx.changed().await.is_err() {
                return;
            }
        }
    }
}

#[derive(Default)]
struct WriteState {
    epoch: u64,
    completed: u64,
    deadline: Option<Instant>,
    aborted: bool,
    finishing: bool,
    buffered: usize,
    awaiting_flush: bool,
}
struct WriteInner {
    state: Mutex<WriteState>,
    changed: Notify,
    io_waker: Arc<AtomicWaker>,
    h2: bool,
}
impl WriteInner {
    fn wake(&self) {
        self.changed.notify_waiters();
        self.io_waker.wake();
    }
    fn complete(state: &mut WriteState) {
        if state.finishing && state.buffered == 0 && !state.awaiting_flush && !state.aborted {
            state.completed = state.epoch;
            state.deadline = None;
            state.finishing = false;
        }
    }
    fn on_flush(&self) {
        let mut state = self.state.lock().unwrap();
        state.awaiting_flush = false;
        Self::complete(&mut state);
        drop(state);
        self.changed.notify_waiters();
    }
    fn drained(&self, epoch: u64) {
        let mut state = self.state.lock().unwrap();
        if state.epoch == epoch && state.buffered > 0 {
            state.buffered -= 1;
            state.awaiting_flush = true;
        }
        drop(state);
        self.wake();
    }
}

/// The response body arms a deadline, emits a batch, then waits for its flush.
/// The transport also observes cancellation while the body is not being polled.
#[derive(Clone)]
pub struct WriteControl(Arc<WriteInner>);
impl WriteControl {
    fn new(h2: bool, io_waker: Arc<AtomicWaker>) -> Self {
        Self(Arc::new(WriteInner {
            state: Mutex::new(WriteState::default()),
            changed: Notify::new(),
            io_waker,
            h2,
        }))
    }
    pub fn begin(&self, timeout: Duration) -> io::Result<u64> {
        let mut state = self.0.state.lock().unwrap();
        if state.aborted {
            return Err(cancelled());
        }
        if state.deadline.is_some() {
            return Err(io::Error::other("previous write batch is still pending"));
        }
        state.epoch = state
            .epoch
            .checked_add(1)
            .ok_or_else(|| io::Error::other("write sequence exhausted"))?;
        state.deadline = Some(Instant::now() + timeout);
        state.finishing = false;
        state.buffered = 0;
        state.awaiting_flush = false;
        let epoch = state.epoch;
        drop(state);
        self.0.wake();
        Ok(epoch)
    }
    /// Call after the final batch frame has been accepted by the transport.
    pub fn finish(&self, epoch: u64) {
        let mut state = self.0.state.lock().unwrap();
        if state.epoch == epoch && state.deadline.is_some() {
            state.finishing = true;
            if !self.0.h2 {
                state.awaiting_flush = true;
            }
            WriteInner::complete(&mut state);
        }
        drop(state);
        self.0.wake();
    }
    pub async fn flushed(&self, epoch: u64) -> io::Result<()> {
        loop {
            let notification = self.0.changed.notified();
            tokio::pin!(notification);
            notification.as_mut().enable();
            {
                let state = self.0.state.lock().unwrap();
                if state.aborted {
                    return Err(cancelled());
                }
                if state.completed >= epoch {
                    return Ok(());
                }
            }
            tokio::select! { _ = notification => {}, error = self.cancelled() => return Err(error) }
        }
    }
    pub fn abort(&self) {
        self.0.state.lock().unwrap().aborted = true;
        self.0.wake();
    }
    pub async fn cancelled(&self) -> io::Error {
        loop {
            let notification = self.0.changed.notified();
            tokio::pin!(notification);
            notification.as_mut().enable();
            let deadline = {
                let state = self.0.state.lock().unwrap();
                if state.aborted {
                    return cancelled();
                }
                state.deadline
            };
            if let Some(deadline) = deadline {
                tokio::select! { _ = notification => {}, _ = tokio::time::sleep_until(deadline) => {
                    // An old timer must not cancel a completed or newer batch.
                    let state = self.0.state.lock().unwrap();
                    if state.deadline == Some(deadline) && deadline <= Instant::now() { return timed_out(); }
                } }
            } else {
                notification.await;
            }
        }
    }
    fn track(&self, bytes: Bytes) -> TrackedData {
        let mut state = self.0.state.lock().unwrap();
        let receipt = if state.deadline.is_some() && !bytes.is_empty() {
            state.buffered += 1;
            Some((self.clone(), state.epoch))
        } else {
            None
        };
        TrackedData { bytes, receipt }
    }
}
fn cancelled() -> io::Error {
    io::Error::new(io::ErrorKind::ConnectionAborted, "response cancelled")
}
fn timed_out() -> io::Error {
    io::Error::new(io::ErrorKind::TimedOut, "response write deadline elapsed")
}

struct TrackedData {
    bytes: Bytes,
    receipt: Option<(WriteControl, u64)>,
}
impl Buf for TrackedData {
    fn remaining(&self) -> usize {
        self.bytes.remaining()
    }
    fn chunk(&self) -> &[u8] {
        self.bytes.chunk()
    }
    fn advance(&mut self, count: usize) {
        self.bytes.advance(count);
        if self.bytes.is_empty() {
            if let Some((control, epoch)) = self.receipt.take() {
                control.0.drained(epoch);
            }
        }
    }
}

#[derive(Clone, Default)]
struct FlushRegistry {
    controls: Arc<Mutex<Vec<Weak<WriteInner>>>>,
    waker: Arc<AtomicWaker>,
}
impl FlushRegistry {
    fn control(&self, h2: bool) -> WriteControl {
        let control = WriteControl::new(h2, self.waker.clone());
        let mut controls = self.controls.lock().unwrap();
        controls.retain(|weak| weak.strong_count() > 0);
        controls.push(Arc::downgrade(&control.0));
        control
    }
    fn flushed(&self) {
        self.controls.lock().unwrap().retain(|weak| {
            if let Some(control) = weak.upgrade() {
                control.on_flush();
                true
            } else {
                false
            }
        });
    }
}

pub struct ControlledIo<I> {
    inner: I,
    prefix: Bytes,
    registry: FlushRegistry,
    control: Option<WriteControl>,
    timer: Pin<Box<Sleep>>,
    deadline: Option<Instant>,
}
impl<I> ControlledIo<I> {
    fn new(
        inner: I,
        prefix: Bytes,
        registry: FlushRegistry,
        control: Option<WriteControl>,
    ) -> Self {
        Self {
            inner,
            prefix,
            registry,
            control,
            timer: Box::pin(tokio::time::sleep(Duration::ZERO)),
            deadline: None,
        }
    }
    fn guard(&mut self, cx: &mut Context<'_>) -> io::Result<()> {
        self.registry.waker.register(cx.waker());
        if let Some(control) = &self.control {
            let state = control.0.state.lock().unwrap();
            if state.aborted {
                return Err(cancelled());
            }
            let deadline = state.deadline;
            drop(state);
            if deadline != self.deadline {
                self.deadline = deadline;
                if let Some(deadline) = deadline {
                    self.timer.as_mut().reset(deadline);
                }
            }
            if self.deadline.is_some() && self.timer.as_mut().poll(cx).is_ready() {
                return Err(timed_out());
            }
        }
        Ok(())
    }
}
impl<I: AsyncRead + Unpin> AsyncRead for ControlledIo<I> {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buffer: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        self.guard(cx)?;
        if !self.prefix.is_empty() {
            let count = buffer.remaining().min(self.prefix.len());
            buffer.put_slice(&self.prefix[..count]);
            self.prefix.advance(count);
            return Poll::Ready(Ok(()));
        }
        Pin::new(&mut self.inner).poll_read(cx, buffer)
    }
}
impl<I: AsyncWrite + Unpin> AsyncWrite for ControlledIo<I> {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bytes: &[u8],
    ) -> Poll<io::Result<usize>> {
        self.guard(cx)?;
        Pin::new(&mut self.inner).poll_write(cx, bytes)
    }
    fn poll_write_vectored(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        slices: &[io::IoSlice<'_>],
    ) -> Poll<io::Result<usize>> {
        self.guard(cx)?;
        Pin::new(&mut self.inner).poll_write_vectored(cx, slices)
    }
    fn is_write_vectored(&self) -> bool {
        self.inner.is_write_vectored()
    }
    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        self.guard(cx)?;
        match Pin::new(&mut self.inner).poll_flush(cx) {
            Poll::Ready(Ok(())) => {
                self.registry.flushed();
                Poll::Ready(Ok(()))
            }
            other => other,
        }
    }
    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_shutdown(cx)
    }
}

#[derive(Clone)]
pub struct InjectControl<S> {
    service: S,
    control: WriteControl,
}
impl<S> Service<Request<hyper::body::Incoming>> for InjectControl<S>
where
    S: Service<Request<Body>, Response = AxumResponse, Error = Infallible>,
{
    type Response = AxumResponse;
    type Error = Infallible;
    type Future = S::Future;
    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.service.poll_ready(cx)
    }
    fn call(&mut self, request: Request<hyper::body::Incoming>) -> Self::Future {
        let mut request = request.map(Body::new);
        request.extensions_mut().insert(self.control.clone());
        self.service.call(request)
    }
}

/// Public listeners follow the negotiated TLS protocol; h2c is opt-in.
#[derive(Clone, Copy)]
pub enum Protocol {
    Http1,
    Alpn,
    PriorKnowledge,
}
pub trait NegotiatedProtocol {
    fn negotiated_h2(&self) -> bool;
}
impl NegotiatedProtocol for TcpStream {
    fn negotiated_h2(&self) -> bool {
        false
    }
}
impl<I: AsyncRead + AsyncWrite + Unpin> NegotiatedProtocol for tokio_rustls::server::TlsStream<I> {
    fn negotiated_h2(&self) -> bool {
        self.get_ref().1.alpn_protocol() == Some(b"h2")
    }
}
#[derive(Clone)]
pub struct Acceptor<A> {
    inner: A,
    shutdown: Shutdown,
    protocol: Protocol,
}
impl<A> Acceptor<A> {
    pub fn new(inner: A, shutdown: Shutdown, protocol: Protocol) -> Self {
        Self {
            inner,
            shutdown,
            protocol,
        }
    }
}
impl<A, S> axum_server::accept::Accept<TcpStream, S> for Acceptor<A>
where
    A: axum_server::accept::Accept<TcpStream, S> + Clone + Send + Sync + 'static,
    A::Future: Send + 'static,
    A::Stream: AsyncRead + AsyncWrite + Unpin + NegotiatedProtocol + Send + 'static,
    A::Service: Service<Request<Body>, Response = AxumResponse, Error = Infallible>
        + Clone
        + Send
        + 'static,
    <A::Service as Service<Request<Body>>>::Future: Send,
    S: Send + 'static,
{
    type Stream = ControlledIo<A::Stream>;
    type Service = InjectControl<A::Service>;
    type Future = Pin<Box<dyn Future<Output = io::Result<(Self::Stream, Self::Service)>> + Send>>;
    fn accept(&self, stream: TcpStream, service: S) -> Self::Future {
        let nodelay = stream.set_nodelay(true);
        let accept = self.inner.accept(stream, service);
        let shutdown = self.shutdown.clone();
        let protocol = self.protocol;
        Box::pin(async move {
            nodelay?;
            let (mut io, service) = tokio::select! { result = accept => result?, _ = shutdown.stopped() => return Err(cancelled()) };
            let prefix = if matches!(protocol, Protocol::PriorKnowledge) {
                tokio::select! {
                    result = tokio::time::timeout(Duration::from_secs(15), protocol_prefix(&mut io)) => result.map_err(|_| timed_out())??,
                    _ = shutdown.stopped() => return Err(cancelled()),
                }
            } else {
                Bytes::new()
            };
            let registry = FlushRegistry::default();
            let h2 = match protocol {
                Protocol::Http1 => false,
                Protocol::Alpn => io.negotiated_h2(),
                Protocol::PriorKnowledge => prefix.as_ref() == H2_PREFACE,
            };
            if h2 {
                let io = ControlledIo::new(io, prefix, registry.clone(), None);
                serve_h2(io, service, registry, shutdown).await?;
                // axum-server's connection watcher owns this acceptor future.
                return Err(io::Error::new(
                    io::ErrorKind::ConnectionAborted,
                    "HTTP2 connection finished",
                ));
            }
            let control = registry.control(false);
            Ok((
                ControlledIo::new(io, prefix, registry, Some(control.clone())),
                InjectControl { service, control },
            ))
        })
    }
}
async fn protocol_prefix<I: AsyncRead + Unpin>(io: &mut I) -> io::Result<Bytes> {
    let mut prefix = Vec::with_capacity(H2_PREFACE.len());
    while prefix.len() < H2_PREFACE.len() {
        let byte = io.read_u8().await?;
        prefix.push(byte);
        if byte != H2_PREFACE[prefix.len() - 1] {
            break;
        }
    }
    Ok(prefix.into())
}

struct ReceiveBody {
    stream: h2::RecvStream,
    remaining: Option<u64>,
    data_done: bool,
    done: bool,
}
impl HttpBody for ReceiveBody {
    type Data = Bytes;
    type Error = io::Error;
    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, io::Error>>> {
        if self.done {
            return Poll::Ready(None);
        }
        if !self.data_done {
            match self.stream.poll_data(cx) {
                Poll::Pending => return Poll::Pending,
                Poll::Ready(Some(Ok(data))) => {
                    if let Some(remaining) = &mut self.remaining {
                        let Some(next) = remaining.checked_sub(data.len() as u64) else {
                            self.done = true;
                            return Poll::Ready(Some(Err(io::Error::new(
                                io::ErrorKind::InvalidData,
                                "HTTP2 body exceeds Content-Length",
                            ))));
                        };
                        *remaining = next;
                    }
                    if let Err(error) = self.stream.flow_control().release_capacity(data.len()) {
                        self.done = true;
                        return Poll::Ready(Some(Err(io::Error::other(error))));
                    }
                    return Poll::Ready(Some(Ok(Frame::data(data))));
                }
                Poll::Ready(Some(Err(error))) => {
                    self.done = true;
                    return Poll::Ready(Some(Err(io::Error::other(error))));
                }
                Poll::Ready(None) => {
                    self.data_done = true;
                    if self.remaining.is_some_and(|remaining| remaining != 0) {
                        self.done = true;
                        return Poll::Ready(Some(Err(io::Error::new(
                            io::ErrorKind::UnexpectedEof,
                            "HTTP2 body shorter than Content-Length",
                        ))));
                    }
                }
            }
        }
        match self.stream.poll_trailers(cx) {
            Poll::Pending => Poll::Pending,
            Poll::Ready(result) => {
                self.done = true;
                Poll::Ready(match result {
                    Ok(Some(headers)) => Some(Ok(Frame::trailers(headers))),
                    Ok(None) => None,
                    Err(error) => Some(Err(io::Error::other(error))),
                })
            }
        }
    }
    fn is_end_stream(&self) -> bool {
        self.done
    }
    fn size_hint(&self) -> SizeHint {
        let mut hint = SizeHint::new();
        if let Some(remaining) = self.remaining {
            hint.set_exact(remaining);
        }
        hint
    }
}
fn content_length(headers: &HeaderMap) -> io::Result<Option<u64>> {
    let mut length = None;
    for value in headers.get_all(header::CONTENT_LENGTH) {
        for value in value
            .to_str()
            .map_err(|_| io::Error::other("invalid Content-Length"))?
            .split(',')
        {
            let value = value.trim();
            if value.is_empty() || !value.bytes().all(|byte| byte.is_ascii_digit()) {
                return Err(io::Error::other("invalid Content-Length"));
            }
            let parsed = value
                .parse::<u64>()
                .map_err(|_| io::Error::other("invalid Content-Length"))?;
            if length.is_some_and(|length| length != parsed) {
                return Err(io::Error::other("conflicting Content-Length"));
            }
            length = Some(parsed);
        }
    }
    Ok(length)
}
fn strip_connection_headers(headers: &mut HeaderMap) {
    let named: Vec<_> = headers
        .get_all(header::CONNECTION)
        .iter()
        .filter_map(|value| value.to_str().ok())
        .flat_map(|value| value.split(','))
        .filter_map(|name| header::HeaderName::from_bytes(name.trim().as_bytes()).ok())
        .collect();
    for name in named {
        headers.remove(name);
    }
    for name in [
        header::CONNECTION,
        header::TRANSFER_ENCODING,
        header::UPGRADE,
        header::TE,
    ] {
        headers.remove(name);
    }
    for name in ["keep-alive", "proxy-connection"] {
        headers.remove(name);
    }
}
async fn serve_h2<I, S>(
    io: I,
    service: S,
    registry: FlushRegistry,
    shutdown: Shutdown,
) -> io::Result<()>
where
    I: AsyncRead + AsyncWrite + Unpin + Send + 'static,
    S: Service<Request<Body>, Response = AxumResponse, Error = Infallible> + Clone + Send + 'static,
    S::Future: Send,
{
    let mut builder = h2::server::Builder::new();
    builder
        .max_concurrent_streams(MAX_STREAMS)
        .max_header_list_size(32 * 1024)
        .max_send_buffer_size(DATA_CHUNK);
    let mut connection = tokio::select! {
        result = tokio::time::timeout(Duration::from_secs(15), builder.handshake::<_, TrackedData>(io)) => result.map_err(|_| timed_out())?.map_err(io::Error::other)?,
        _ = shutdown.stopped() => return Ok(()),
    };
    let mut tasks = JoinSet::new();
    let result = loop {
        tokio::select! {
            biased;
            _ = shutdown.stopped() => break Ok(()),
            result = tasks.join_next(), if !tasks.is_empty() => { if let Some(Err(error)) = result { break Err(io::Error::other(error)); } },
            request = connection.accept() => {
                let Some(request) = request else { break Ok(()); };
                let (request, mut respond) = match request { Ok(request) => request, Err(error) => break Err(io::Error::other(error)) };
                if tasks.len() >= MAX_STREAMS as usize { respond.send_reset(h2::Reason::REFUSED_STREAM); continue; }
                let remaining = match content_length(request.headers()) { Ok(length) => length, Err(_) => { respond.send_reset(h2::Reason::PROTOCOL_ERROR); continue; } };
                let control = registry.control(true);
                let method = request.method().clone();
                let mut request = request.map(|stream| Body::new(ReceiveBody { stream, remaining, data_done: false, done: false }));
                request.extensions_mut().insert(control.clone());
                let service = service.clone();
                tasks.spawn(async move { respond_h2(service, request, method, respond, control).await; });
            }
        }
    };
    // Response futures own bodies, registrations and queued native frame owners.
    // JoinSet also aborts them on a dropped acceptor future.
    tasks.abort_all();
    while tasks.join_next().await.is_some() {}
    drop(connection);
    result
}
async fn respond_h2<S>(
    service: S,
    request: Request<Body>,
    method: Method,
    mut respond: h2::server::SendResponse<TrackedData>,
    control: WriteControl,
) where
    S: Service<Request<Body>, Response = AxumResponse, Error = Infallible>,
    S::Future: Send,
{
    let response = tokio::select! {
        _ = control.cancelled() => { respond.send_reset(h2::Reason::CANCEL); return; },
        _ = poll_fn(|cx| respond.poll_reset(cx)) => return,
        result = service.oneshot(request) => match result { Ok(response) => response, Err(error) => match error {} },
    };
    let (parts, mut body) = response.into_parts();
    let mut head = Response::from_parts(parts, ());
    strip_connection_headers(head.headers_mut());
    head.headers_mut().entry(header::DATE).or_insert_with(|| {
        HeaderValue::from_str(&httpdate::fmt_http_date(SystemTime::now())).unwrap()
    });
    let forbidden_body =
        head.status().is_informational() || matches!(head.status().as_u16(), 204 | 304);
    if !forbidden_body && !head.headers().contains_key(header::CONTENT_LENGTH) {
        if let Some(length) = body.size_hint().exact() {
            head.headers_mut()
                .insert(header::CONTENT_LENGTH, HeaderValue::from(length));
        }
    }
    if head.status().as_u16() == 204 {
        head.headers_mut().remove(header::CONTENT_LENGTH);
    }
    let end = method == Method::HEAD || forbidden_body || body.is_end_stream();
    let Ok(mut send) = respond.send_response(head, end) else {
        return;
    };
    if end {
        return;
    }
    let result = tokio::select! {
        _ = control.cancelled() => Err(()),
        result = pipe_h2(&mut body, &mut send, &control) => result,
    };
    if result.is_err() {
        control.abort();
        send.send_reset(h2::Reason::CANCEL);
    }
}
async fn pipe_h2(
    body: &mut Body,
    send: &mut h2::SendStream<TrackedData>,
    control: &WriteControl,
) -> Result<(), ()> {
    loop {
        let frame = tokio::select! {
            _ = poll_fn(|cx| send.poll_reset(cx)) => return Err(()),
            frame = poll_fn(|cx| Pin::new(&mut *body).poll_frame(cx)) => frame,
        };
        let Some(frame) = frame else {
            break;
        };
        let frame = frame.map_err(|_| ())?;
        match frame.into_data() {
            Ok(mut bytes) => {
                while !bytes.is_empty() {
                    let count = bytes.len().min(DATA_CHUNK);
                    send.reserve_capacity(count);
                    let capacity = poll_fn(|cx| send.poll_capacity(cx))
                        .await
                        .ok_or(())?
                        .map_err(|_| ())?;
                    let count = capacity.min(count);
                    // A reset cannot retract the codec's in-flight DATA frame.
                    // Bound that retained allocation without pinning the entire
                    // native frame through a tiny Bytes slice after cancellation.
                    let chunk = Bytes::copy_from_slice(&bytes[..count]);
                    bytes.advance(count);
                    send.send_data(control.track(chunk), false)
                        .map_err(|_| ())?;
                }
                send.reserve_capacity(0);
            }
            Err(frame) => {
                if let Ok(mut trailers) = frame.into_trailers() {
                    strip_connection_headers(&mut trailers);
                    send.send_trailers(trailers).map_err(|_| ())?;
                    return Ok(());
                }
            }
        }
    }
    send.send_data(control.track(Bytes::new()), true)
        .map_err(|_| ())
}

#[cfg(test)]
#[path = "transport_tests.rs"]
mod tests;
