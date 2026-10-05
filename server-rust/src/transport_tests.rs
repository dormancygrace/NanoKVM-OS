use super::*;
use axum::{
    extract::ConnectInfo,
    routing::{any, get},
    Router,
};
use std::{
    net::SocketAddr,
    sync::atomic::{AtomicBool, Ordering},
};
use tokio::{io::AsyncWriteExt, task::JoinHandle};

struct Owner {
    bytes: Vec<u8>,
    dropped: Arc<AtomicBool>,
}
impl AsRef<[u8]> for Owner {
    fn as_ref(&self) -> &[u8] {
        &self.bytes
    }
}
impl Drop for Owner {
    fn drop(&mut self) {
        self.dropped.store(true, Ordering::Release);
    }
}
struct DropMark(Arc<AtomicBool>);
impl Drop for DropMark {
    fn drop(&mut self) {
        self.0.store(true, Ordering::Release);
    }
}
struct BatchBody {
    bytes: Option<Bytes>,
    control: WriteControl,
    timeout: Duration,
    epoch: Option<u64>,
    wait: Option<Pin<Box<dyn Future<Output = io::Result<()>> + Send>>>,
    completed: Arc<AtomicBool>,
    _mark: DropMark,
}
impl HttpBody for BatchBody {
    type Data = Bytes;
    type Error = io::Error;
    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, io::Error>>> {
        if let Some(bytes) = self.bytes.take() {
            match self.control.begin(self.timeout) {
                Ok(epoch) => {
                    self.epoch = Some(epoch);
                    return Poll::Ready(Some(Ok(Frame::data(bytes))));
                }
                Err(error) => return Poll::Ready(Some(Err(error))),
            }
        }
        if let Some(epoch) = self.epoch.take() {
            self.control.finish(epoch);
            let control = self.control.clone();
            self.wait = Some(Box::pin(async move { control.flushed(epoch).await }));
        }
        if let Some(wait) = self.wait.as_mut() {
            match wait.as_mut().poll(cx) {
                Poll::Pending => return Poll::Pending,
                Poll::Ready(Err(error)) => {
                    self.wait = None;
                    return Poll::Ready(Some(Err(error)));
                }
                Poll::Ready(Ok(())) => {
                    self.wait = None;
                    self.completed.store(true, Ordering::Release);
                }
            }
        }
        Poll::Ready(None)
    }
}
#[derive(Clone)]
struct Marks {
    owner: Arc<AtomicBool>,
    body: Arc<AtomicBool>,
    flushed: Arc<AtomicBool>,
    control: Arc<Mutex<Option<WriteControl>>>,
}
impl Marks {
    fn new() -> Self {
        Self {
            owner: Arc::new(AtomicBool::new(false)),
            body: Arc::new(AtomicBool::new(false)),
            flushed: Arc::new(AtomicBool::new(false)),
            control: Arc::new(Mutex::new(None)),
        }
    }
    fn router(&self, size: usize, timeout: Duration) -> Router {
        let marks = self.clone();
        Router::new()
            .route(
                "/slow",
                get(move |request: Request<Body>| {
                    let marks = marks.clone();
                    async move {
                        let control = request.extensions().get::<WriteControl>().unwrap().clone();
                        *marks.control.lock().unwrap() = Some(control.clone());
                        let mut data = vec![b'x'; size];
                        let boundary = b"\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n";
                        data.extend_from_slice(boundary);
                        let bytes = Bytes::from_owner(Owner {
                            bytes: data,
                            dropped: marks.owner,
                        });
                        let body = BatchBody {
                            bytes: Some(bytes),
                            control,
                            timeout,
                            epoch: None,
                            wait: None,
                            completed: marks.flushed,
                            _mark: DropMark(marks.body),
                        };
                        Response::builder()
                            .header(
                                header::CONTENT_TYPE,
                                "multipart/x-mixed-replace;boundary=frame",
                            )
                            .header(header::CONNECTION, "keep-alive, x-hop")
                            .header("x-hop", "remove")
                            .header("x-end", "retain")
                            .body(Body::new(body))
                            .unwrap()
                    }
                }),
            )
            .route(
                "/fast",
                get(|ConnectInfo(peer): ConnectInfo<SocketAddr>| async move {
                    assert!(peer.ip().is_loopback());
                    Response::builder()
                        .header("x-live", "alive")
                        .body(Body::from("alive"))
                        .unwrap()
                }),
            )
    }
}
#[derive(Clone)]
struct SmallSocket;
impl<S> axum_server::accept::Accept<TcpStream, S> for SmallSocket {
    type Stream = TcpStream;
    type Service = S;
    type Future = std::future::Ready<io::Result<(TcpStream, S)>>;
    fn accept(&self, stream: TcpStream, service: S) -> Self::Future {
        use std::os::fd::AsRawFd;
        let size: libc::c_int = 8192;
        let result = unsafe {
            libc::setsockopt(
                stream.as_raw_fd(),
                libc::SOL_SOCKET,
                libc::SO_SNDBUF,
                (&size as *const libc::c_int).cast(),
                std::mem::size_of_val(&size) as libc::socklen_t,
            )
        };
        std::future::ready(if result == 0 {
            Ok((stream, service))
        } else {
            Err(io::Error::last_os_error())
        })
    }
}
struct Server {
    address: SocketAddr,
    handle: axum_server::Handle<SocketAddr>,
    shutdown: Shutdown,
    task: JoinHandle<io::Result<()>>,
}
impl Server {
    async fn start(router: Router, h2: bool) -> Self {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        listener.set_nonblocking(true).unwrap();
        let handle = axum_server::Handle::new();
        let shutdown = Shutdown::default();
        let mut server = axum_server::from_tcp(listener)
            .unwrap()
            .acceptor(Acceptor::new(
                SmallSocket,
                shutdown.clone(),
                if h2 {
                    Protocol::PriorKnowledge
                } else {
                    Protocol::Http1
                },
            ))
            .handle(handle.clone());
        if !h2 {
            server = server.http1_only();
        }
        let task =
            tokio::spawn(server.serve(router.into_make_service_with_connect_info::<SocketAddr>()));
        assert_eq!(handle.listening().await, Some(address));
        Self {
            address,
            handle,
            shutdown,
            task,
        }
    }
    async fn stop(mut self) {
        self.shutdown.stop();
        self.handle.graceful_shutdown(Some(Duration::from_secs(1)));
        tokio::time::timeout(Duration::from_secs(2), &mut self.task)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
    }
}
impl Drop for Server {
    fn drop(&mut self) {
        self.shutdown.stop();
        self.handle.shutdown();
    }
}
async fn until(mark: &AtomicBool) {
    tokio::time::timeout(Duration::from_secs(2), async {
        while !mark.load(Ordering::Acquire) {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
}
async fn raw_request(address: SocketAddr, request: &[u8]) -> Vec<u8> {
    let mut stream = TcpStream::connect(address).await.unwrap();
    stream.write_all(request).await.unwrap();
    let mut result = Vec::new();
    tokio::time::timeout(Duration::from_secs(2), stream.read_to_end(&mut result))
        .await
        .unwrap()
        .unwrap();
    result
}

#[tokio::test]
async fn http1_slow_socket_deadline_drops_body_and_whole_frame_owner() {
    let marks = Marks::new();
    let server = Server::start(
        marks.router(8 * 1024 * 1024, Duration::from_millis(180)),
        false,
    )
    .await;
    let mut stream = TcpStream::connect(server.address).await.unwrap();
    stream
        .write_all(b"GET /slow HTTP/1.1\r\nHost: localhost\r\n\r\n")
        .await
        .unwrap();
    // No reads: the owned socket's 8KiB send buffer becomes blocked.
    until(&marks.body).await;
    until(&marks.owner).await;
    assert!(!marks.flushed.load(Ordering::Acquire));
    let control = marks.control.lock().unwrap().as_ref().unwrap().clone();
    assert_eq!(control.cancelled().await.kind(), io::ErrorKind::TimedOut);
    drop(stream);
    server.stop().await;
}

#[tokio::test]
async fn http1_following_boundary_flush_completes_before_deadline() {
    let marks = Marks::new();
    let server = Server::start(marks.router(4096, Duration::from_secs(1)), false).await;
    let response = raw_request(
        server.address,
        b"GET /slow HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
    )
    .await;
    assert!(response.starts_with(b"HTTP/1.1 200"));
    assert!(response
        .windows(b"\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n".len())
        .any(|window| window == b"\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n"));
    until(&marks.flushed).await;
    until(&marks.owner).await;
    until(&marks.body).await;
    let control = marks.control.lock().unwrap().as_ref().unwrap().clone();
    assert!(
        tokio::time::timeout(Duration::from_millis(20), control.cancelled())
            .await
            .is_err()
    );
    server.stop().await;
}

type Client = h2::client::SendRequest<Bytes>;
async fn h2_client(address: SocketAddr) -> (Client, JoinHandle<Result<(), h2::Error>>) {
    let stream = TcpStream::connect(address).await.unwrap();
    h2_io(stream).await
}
async fn h2_io<I: AsyncRead + AsyncWrite + Unpin + Send + 'static>(
    stream: I,
) -> (Client, JoinHandle<Result<(), h2::Error>>) {
    let mut builder = h2::client::Builder::new();
    builder.initial_connection_window_size(1024 * 1024);
    let (client, connection) = builder.handshake(stream).await.unwrap();
    (client, tokio::spawn(connection))
}
async fn request(client: &mut Client, method: Method, path: &str) -> h2::client::ResponseFuture {
    poll_fn(|cx| client.poll_ready(cx)).await.unwrap();
    client
        .send_request(
            Request::builder()
                .method(method)
                .uri(format!("https://localhost{path}"))
                .body(())
                .unwrap(),
            true,
        )
        .unwrap()
        .0
}
async fn h2_bytes(mut stream: h2::RecvStream) -> Vec<u8> {
    let mut bytes = Vec::new();
    while let Some(data) = stream.data().await {
        let data = data.unwrap();
        bytes.extend_from_slice(&data);
        stream.flow_control().release_capacity(data.len()).unwrap();
    }
    bytes
}
async fn live(client: &mut Client) {
    let response = tokio::time::timeout(
        Duration::from_secs(1),
        request(client, Method::GET, "/fast").await,
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(response.status(), 200);
    assert_eq!(response.headers()["x-live"], "alive");
    assert!(response.headers().contains_key(header::DATE));
    assert_eq!(response.headers()[header::CONTENT_LENGTH], "5");
    assert_eq!(h2_bytes(response.into_body()).await, b"alive");
}
async fn blocked_h2(abort: bool, tls: bool) {
    let marks = Marks::new();
    let timeout = if abort {
        Duration::from_secs(30)
    } else {
        if tls {
            Duration::from_secs(5)
        } else {
            Duration::from_millis(250)
        }
    };
    let server = if tls {
        Server::start_tls(marks.router(8 * 1024 * 1024, timeout)).await
    } else {
        Server::start(marks.router(8 * 1024 * 1024, timeout), true).await
    };
    let (mut client, connection) = if tls {
        h2_io(tls_stream(server.address, b"h2").await).await
    } else {
        h2_client(server.address).await
    };
    let start = Instant::now();
    let response = request(&mut client, Method::GET, "/slow")
        .await
        .await
        .unwrap();
    assert_eq!(response.status(), 200);
    assert!(!response.headers().contains_key(header::CONNECTION));
    assert!(!response.headers().contains_key("x-hop"));
    assert_eq!(response.headers()["x-end"], "retain");
    let mut slow = response.into_body();
    // Keep per-stream credits exhausted while allowing peer stream credits.
    let first = slow.data().await.unwrap().unwrap();
    assert!(!first.is_empty());
    assert!(!marks.flushed.load(Ordering::Acquire));
    live(&mut client).await;
    if abort {
        marks.control.lock().unwrap().as_ref().unwrap().abort();
    }
    let reason = tokio::time::timeout(Duration::from_secs(6), async {
        loop {
            match slow.data().await {
                Some(Ok(_data)) => {} // deliberately do not release stream credit
                Some(Err(error)) => return error.reason(),
                None => panic!("blocked stream ended without reset"),
            }
        }
    })
    .await
    .unwrap();
    assert_eq!(reason, Some(h2::Reason::CANCEL));
    if tls {
        assert!(start.elapsed() >= Duration::from_millis(4700));
    }
    until(&marks.owner).await;
    until(&marks.body).await;
    assert!(!marks.flushed.load(Ordering::Acquire));
    live(&mut client).await;
    drop(slow);
    drop(client);
    server.stop().await;
    tokio::time::timeout(Duration::from_secs(1), connection)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
}

#[tokio::test]
async fn http2_flow_blocked_deadline_resets_only_one_stream_and_releases_owner() {
    blocked_h2(false, false).await;
}
#[tokio::test]
async fn http2_flow_blocked_revocation_resets_only_one_stream_and_releases_owner() {
    blocked_h2(true, false).await;
}

#[tokio::test]
async fn http2_following_boundary_waits_for_flush_and_preserves_head() {
    let marks = Marks::new();
    let server = Server::start(marks.router(32768, Duration::from_secs(1)), true).await;
    let (mut client, connection) = h2_client(server.address).await;
    let response = request(&mut client, Method::GET, "/slow")
        .await
        .await
        .unwrap();
    let bytes = h2_bytes(response.into_body()).await;
    assert_eq!(
        bytes.len(),
        32768 + b"\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n".len()
    );
    assert!(bytes.ends_with(b"\r\n--frame\r\nContent-Type: image/jpeg\r\n\r\n"));
    until(&marks.flushed).await;
    until(&marks.owner).await;
    until(&marks.body).await;
    let response = request(&mut client, Method::HEAD, "/fast")
        .await
        .await
        .unwrap();
    assert_eq!(response.headers()[header::CONTENT_LENGTH], "5");
    assert!(h2_bytes(response.into_body()).await.is_empty());
    drop(client);
    server.stop().await;
    tokio::time::timeout(Duration::from_secs(1), connection)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
}

#[tokio::test]
async fn http2_request_data_and_trailers_release_flow_control_and_keep_connect_info() {
    let router = Router::new().route(
        "/echo",
        any(|request: Request<Body>| async move {
            assert!(request
                .extensions()
                .get::<ConnectInfo<SocketAddr>>()
                .unwrap()
                .0
                .ip()
                .is_loopback());
            let mut body = request.into_body();
            let mut data = Vec::new();
            let mut trailer = false;
            while let Some(frame) = poll_fn(|cx| Pin::new(&mut body).poll_frame(cx)).await {
                let frame = frame.unwrap();
                match frame.into_data() {
                    Ok(bytes) => data.extend_from_slice(&bytes),
                    Err(frame) => {
                        if let Ok(headers) = frame.into_trailers() {
                            assert_eq!(headers["x-proof"], "complete");
                            trailer = true;
                        }
                    }
                }
            }
            assert!(trailer);
            Body::from(data)
        }),
    );
    let server = Server::start(router, true).await;
    let (mut client, connection) = h2_client(server.address).await;
    poll_fn(|cx| client.poll_ready(cx)).await.unwrap();
    let (response, mut send) = client
        .send_request(
            Request::builder()
                .method(Method::POST)
                .uri("https://localhost/echo")
                .header(header::CONTENT_LENGTH, 131072)
                .body(())
                .unwrap(),
            false,
        )
        .unwrap();
    let sender = tokio::spawn(async move {
        let mut count = 131072;
        while count > 0 {
            send.reserve_capacity(count.min(DATA_CHUNK));
            let capacity = poll_fn(|cx| send.poll_capacity(cx)).await.unwrap().unwrap();
            let size = count.min(capacity).min(DATA_CHUNK);
            send.send_data(Bytes::from(vec![b'z'; size]), false)
                .unwrap();
            count -= size;
        }
        let mut trailers = HeaderMap::new();
        trailers.insert("x-proof", HeaderValue::from_static("complete"));
        send.send_trailers(trailers).unwrap();
    });
    let response = tokio::time::timeout(Duration::from_secs(2), response)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(h2_bytes(response.into_body()).await, vec![b'z'; 131072]);
    sender.await.unwrap();
    drop(client);
    server.stop().await;
    tokio::time::timeout(Duration::from_secs(1), connection)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
}

#[tokio::test]
async fn http2_peer_reset_cancels_pending_handler_and_shutdown_joins_bodies() {
    let entered = Arc::new(AtomicBool::new(false));
    let dropped = Arc::new(AtomicBool::new(false));
    let request_entered = entered.clone();
    let request_dropped = dropped.clone();
    let marks = Marks::new();
    let router = marks
        .router(8 * 1024 * 1024, Duration::from_secs(30))
        .route(
            "/wait",
            get(move || {
                let entered = request_entered.clone();
                let dropped = request_dropped.clone();
                async move {
                    let _mark = DropMark(dropped);
                    entered.store(true, Ordering::Release);
                    std::future::pending::<Body>().await
                }
            }),
        );
    let server = Server::start(router, true).await;
    let (mut client, connection) = h2_client(server.address).await;
    poll_fn(|cx| client.poll_ready(cx)).await.unwrap();
    let (response, mut send) = client
        .send_request(
            Request::builder()
                .uri("https://localhost/wait")
                .body(())
                .unwrap(),
            true,
        )
        .unwrap();
    until(&entered).await;
    send.send_reset(h2::Reason::CANCEL);
    assert!(response.await.is_err());
    until(&dropped).await;
    let response = request(&mut client, Method::GET, "/slow")
        .await
        .await
        .unwrap();
    let _slow = response.into_body();
    live(&mut client).await;
    server.stop().await;
    until(&marks.body).await;
    until(&marks.owner).await;
    drop(client);
    let _ = tokio::time::timeout(Duration::from_secs(1), connection)
        .await
        .unwrap();
}

#[tokio::test]
async fn protocol_sniff_replays_short_http1_and_plain_listener_rejects_h2c() {
    let server = Server::start(Marks::new().router(1, Duration::from_secs(1)), true).await;
    let response = raw_request(server.address, b"GET /fast HTTP/1.0\r\n\r\n").await;
    assert!(response.starts_with(b"HTTP/1.0 200"));
    assert!(response.ends_with(b"alive"));
    server.stop().await;
    let server = Server::start(Marks::new().router(1, Duration::from_secs(1)), false).await;
    let response = raw_request(server.address, H2_PREFACE).await;
    assert!(!response.starts_with(H2_PREFACE));
    assert!(
        response.is_empty(),
        "unsupported h2c must close without HTTP2 frames"
    );
    server.stop().await;
}

async fn tls_stream(
    address: SocketAddr,
    protocol: &[u8],
) -> tokio_rustls::client::TlsStream<TcpStream> {
    let mut roots = rustls::RootCertStore::empty();
    roots
        .add(rustls::pki_types::CertificateDer::from(
            include_bytes!("../tests/fixtures/transport-cert.der").to_vec(),
        ))
        .unwrap();
    let mut config = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .unwrap()
    .with_root_certificates(roots)
    .with_no_client_auth();
    config.alpn_protocols = vec![protocol.to_vec()];
    let connector = tokio_rustls::TlsConnector::from(Arc::new(config));
    let stream = connector
        .connect(
            rustls::pki_types::ServerName::try_from("localhost").unwrap(),
            TcpStream::connect(address).await.unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(stream.get_ref().1.alpn_protocol(), Some(protocol));
    stream
}
impl Server {
    async fn start_tls(router: Router) -> Self {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        listener.set_nonblocking(true).unwrap();
        let handle = axum_server::Handle::new();
        let shutdown = Shutdown::default();
        let key = rustls::pki_types::PrivatePkcs8KeyDer::from(
            include_bytes!("../tests/fixtures/transport-key.der").to_vec(),
        );
        let mut config = rustls::ServerConfig::builder_with_provider(Arc::new(
            rustls::crypto::ring::default_provider(),
        ))
        .with_safe_default_protocol_versions()
        .unwrap()
        .with_no_client_auth()
        .with_single_cert(
            vec![rustls::pki_types::CertificateDer::from(
                include_bytes!("../tests/fixtures/transport-cert.der").to_vec(),
            )],
            key.into(),
        )
        .unwrap();
        config.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];
        let tls = axum_server::tls_rustls::RustlsConfig::from_config(Arc::new(config));
        let server = axum_server::from_tcp_rustls(listener, tls)
            .unwrap()
            .map(|inner| {
                Acceptor::new(
                    inner.acceptor(SmallSocket),
                    shutdown.clone(),
                    Protocol::Alpn,
                )
            })
            .http1_only()
            .handle(handle.clone());
        let task =
            tokio::spawn(server.serve(router.into_make_service_with_connect_info::<SocketAddr>()));
        assert_eq!(handle.listening().await, Some(address));
        Self {
            address,
            handle,
            shutdown,
            task,
        }
    }
}
#[tokio::test]
async fn tls_http2_actual_five_second_deadline_keeps_peer_stream_alive() {
    blocked_h2(false, true).await;
}

#[tokio::test]
async fn tls_http1_alpn_serves_http1_and_rejects_h2_preface() {
    let server = Server::start_tls(Marks::new().router(1, Duration::from_secs(1))).await;
    let mut stream = tls_stream(server.address, b"http/1.1").await;
    stream
        .write_all(b"GET /fast HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .await
        .unwrap();
    let mut bytes = Vec::new();
    // rustls reports missing close_notify after the completed HTTP1 response.
    let result = tokio::time::timeout(Duration::from_secs(2), stream.read_to_end(&mut bytes))
        .await
        .unwrap();
    if let Err(error) = result {
        assert_eq!(error.kind(), io::ErrorKind::UnexpectedEof);
    }
    assert!(bytes.starts_with(b"HTTP/1.1 200"));
    assert!(bytes.ends_with(b"alive"));
    let mut stream = tls_stream(server.address, b"http/1.1").await;
    stream.write_all(H2_PREFACE).await.unwrap();
    let mut bytes = Vec::new();
    let result = tokio::time::timeout(Duration::from_secs(2), stream.read_to_end(&mut bytes))
        .await
        .unwrap();
    if let Err(error) = result {
        assert_eq!(error.kind(), io::ErrorKind::UnexpectedEof);
    }
    assert!(
        bytes.is_empty(),
        "ALPN HTTP1 must close instead of accepting an HTTP2 preface"
    );
    server.stop().await;
}
