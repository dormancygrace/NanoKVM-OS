use super::*;
use crate::{
    app,
    transport::{Acceptor, Protocol},
    video_source::tests::Fixture,
};
use futures_util::future::poll_fn;
use std::net::SocketAddr;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::TcpStream,
};
const JPEG: &[u8] = include_bytes!("../tests/fixtures/mjpeg-red-app9.jpg");
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
    task: tokio::task::JoinHandle<io::Result<()>>,
}
impl Server {
    async fn start(fixture: &Fixture, h2: bool) -> Self {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        listener.set_nonblocking(true).unwrap();
        let handle = axum_server::Handle::new();
        let shutdown = fixture.runtime.transport_shutdown();
        let server = axum_server::from_tcp(listener)
            .unwrap()
            .acceptor(Acceptor::new(
                SmallSocket,
                shutdown,
                if h2 {
                    Protocol::PriorKnowledge
                } else {
                    Protocol::Http1
                },
            ))
            .http1_only()
            .handle(handle.clone());
        let router = app(fixture.runtime.clone(), fixture.root.path().join("web"));
        let task =
            tokio::spawn(server.serve(router.into_make_service_with_connect_info::<SocketAddr>()));
        assert_eq!(handle.listening().await, Some(address));
        Self {
            address,
            handle,
            task,
        }
    }

    async fn start_tls(fixture: &Fixture) -> Self {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        listener.set_nonblocking(true).unwrap();
        let handle = axum_server::Handle::new();
        let shutdown = fixture.runtime.transport_shutdown();
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
        let router = app(fixture.runtime.clone(), fixture.root.path().join("web"));
        let task =
            tokio::spawn(server.serve(router.into_make_service_with_connect_info::<SocketAddr>()));
        assert_eq!(handle.listening().await, Some(address));
        Self {
            address,
            handle,
            task,
        }
    }
    async fn stop(mut self) {
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
        self.handle.shutdown();
    }
}
struct Reader {
    stream: TcpStream,
    head: String,
    first: bool,
}
async fn line(stream: &mut TcpStream, max: usize) -> Vec<u8> {
    let mut data = Vec::new();
    loop {
        assert!(data.len() <= max);
        data.push(stream.read_u8().await.unwrap());
        if data.ends_with(b"\r\n") {
            return data;
        }
    }
}
impl Reader {
    async fn open(address: SocketAddr) -> Self {
        Self::with_token(address, "").await
    }
    async fn with_token(address: SocketAddr, token: &str) -> Self {
        let mut stream = TcpStream::connect(address).await.unwrap();
        stream
            .write_all(format!("GET /api/stream/mjpeg HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer {token}\r\n\r\n").as_bytes())
            .await
            .unwrap();
        let head = tokio::time::timeout(Duration::from_secs(3), async {
            let mut data = Vec::new();
            loop {
                let next = line(&mut stream, 16 * 1024).await;
                let done = next == b"\r\n";
                data.extend_from_slice(&next);
                assert!(data.len() < 32 * 1024);
                if done {
                    break;
                }
            }
            String::from_utf8(data).unwrap()
        })
        .await
        .unwrap();
        assert!(head.starts_with("HTTP/1.1 200"), "{head}");
        assert!(head
            .to_ascii_lowercase()
            .contains("transfer-encoding: chunked"));
        Self {
            stream,
            head,
            first: true,
        }
    }
    async fn frame(&mut self) -> Bytes {
        let data = tokio::time::timeout(Duration::from_secs(3), async {
            let mut data = Vec::new();
            loop {
                let header = line(&mut self.stream, 128).await;
                let length = usize::from_str_radix(
                    std::str::from_utf8(&header[..header.len() - 2])
                        .unwrap()
                        .split(';')
                        .next()
                        .unwrap(),
                    16,
                )
                .unwrap();
                assert!(length > 0 && length <= 16 * 1024 * 1024);
                let start = data.len();
                data.resize(start + length, 0);
                self.stream.read_exact(&mut data[start..]).await.unwrap();
                let mut ending = [0; 2];
                self.stream.read_exact(&mut ending).await.unwrap();
                assert_eq!(&ending, b"\r\n");
                if data.ends_with(NEXT) && data.len() > NEXT.len() {
                    break;
                }
            }
            data
        })
        .await
        .unwrap();
        let start = if self.first {
            assert!(data.starts_with(PART));
            PART.len()
        } else {
            0
        };
        self.first = false;
        Bytes::copy_from_slice(&data[start..data.len() - NEXT.len()])
    }
}
async fn slots(fixture: &Fixture) {
    tokio::time::timeout(Duration::from_secs(8), async {
        while fixture.runtime.socket_slots.available_permits() != 64 {
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .unwrap();
    fixture.runtime.mjpeg.join().await;
}
#[tokio::test]
async fn actual_http1_api_multipart_headers_first_flush_and_new_static_viewer_share_native_reader()
{
    let fixture = Fixture::new_mjpeg(JPEG);
    fixture.initialize().await;
    let server = Server::start(&fixture, false).await;
    let mut first = Reader::open(server.address).await;
    let head = first.head.to_ascii_lowercase();
    for value in [
        "content-type: multipart/x-mixed-replace; boundary=frame",
        "cache-control: no-cache",
        "connection: keep-alive",
        "pragma: no-cache",
        "x-server-date:",
    ] {
        assert!(head.contains(value), "{head}");
    }
    assert!(!head.contains("content-length:"));
    assert!(same_image(&first.frame().await, JPEG));
    let mut second = Reader::open(server.address).await;
    assert!(same_image(&second.frame().await, JPEG));
    assert_eq!(fixture.runtime.mjpeg.lock().entries.len(), 2);
    assert_eq!(fixture.runtime.socket_slots.available_permits(), 62);
    assert!(!fixture.backend.actor().stopped());
    drop(first);
    drop(second);
    slots(&fixture).await;
    assert_eq!(fixture.budget.used(), 0);
    assert!(!fixture.backend.actor().stopped());
    fixture.cleanup().await;
    server.stop().await;
}
#[tokio::test]
async fn actual_http1_mjpeg_revoke_is_independent_of_four_busy_api_jobs() {
    let fixture = Fixture::new_mjpeg(JPEG);
    fixture.initialize().await;
    let server = Server::start(&fixture, false).await;
    let mut reader = Reader::open(server.address).await;
    drop(reader.frame().await);
    let permits: Vec<_> = (0..4)
        .map(|_| fixture.runtime.jobs.clone().try_acquire_owned().unwrap())
        .collect();
    fixture.runtime.revoke_sessions("admin");
    slots(&fixture).await;
    assert_eq!(fixture.budget.used(), 0);
    assert!(!fixture.backend.actor().stopped());
    drop(permits);
    drop(reader);
    fixture.cleanup().await;
    server.stop().await;
}
#[tokio::test]
async fn actual_http1_native_mjpeg_slow_writer_times_out_after_five_seconds_and_releases_owners() {
    let mut large = JPEG.to_vec();
    large.resize(8 * 1024 * 1024, b'x');
    let fixture = Fixture::new_mjpeg(&large);
    fixture.budget.set_limit(64 * 1024 * 1024);
    fixture.runtime.screen.set("fps", 10).unwrap();
    fixture.initialize().await;
    let server = Server::start(&fixture, false).await;
    let reader = Reader::open(server.address).await;
    fixture.until_trace("mjpeg:").await;
    let start = Instant::now();
    slots(&fixture).await;
    assert!(start.elapsed() >= Duration::from_millis(4700));
    assert_eq!(fixture.budget.used(), 0);
    assert!(!fixture.backend.actor().stopped());
    drop(reader);
    fixture.cleanup().await;
    server.stop().await;
}
async fn h2_response(
    client: &mut h2::client::SendRequest<Bytes>,
    path: &str,
) -> h2::client::ResponseFuture {
    poll_fn(|cx| client.poll_ready(cx)).await.unwrap();
    client
        .send_request(
            axum::http::Request::builder()
                .uri(format!("https://localhost{path}"))
                .body(())
                .unwrap(),
            true,
        )
        .unwrap()
        .0
}
async fn receive(mut stream: h2::RecvStream) -> Vec<u8> {
    let mut data = Vec::new();
    while let Some(bytes) = stream.data().await {
        let bytes = bytes.unwrap();
        data.extend_from_slice(&bytes);
        stream.flow_control().release_capacity(bytes.len()).unwrap();
    }
    data
}
#[tokio::test]
async fn actual_http2_native_mjpeg_blocked_revoke_releases_sealed_frame_and_keeps_peer_alive_under_busy_jobs(
) {
    let mut large = JPEG.to_vec();
    large.resize(8 * 1024 * 1024, b'x');
    let fixture = Fixture::new_mjpeg(&large);
    fixture.budget.set_limit(64 * 1024 * 1024);
    fixture.runtime.screen.set("fps", 10).unwrap();
    fixture.initialize().await;
    let server = Server::start_tls(&fixture).await;
    let stream = tls_stream(server.address).await;
    let mut builder = h2::client::Builder::new();
    builder.initial_connection_window_size(1024 * 1024);
    let (mut client, connection) = builder.handshake::<_, Bytes>(stream).await.unwrap();
    let connection = tokio::spawn(connection);
    let response = h2_response(&mut client, "/api/stream/mjpeg")
        .await
        .await
        .unwrap();
    assert_eq!(response.status(), 200);
    assert!(!response.headers().contains_key(header::CONNECTION));
    let mut slow = response.into_body();
    assert!(slow.data().await.unwrap().is_ok());
    let permits: Vec<_> = (0..4)
        .map(|_| fixture.runtime.jobs.clone().try_acquire_owned().unwrap())
        .collect();
    let peer = h2_response(&mut client, "/").await.await.unwrap();
    assert_eq!(peer.status(), 200);
    assert_eq!(receive(peer.into_body()).await, b"fixture");
    fixture.runtime.revoke_sessions("admin");
    let reason = tokio::time::timeout(Duration::from_secs(2), async {
        loop {
            match slow.data().await {
                Some(Ok(_)) => {}
                Some(Err(error)) => return error.reason(),
                None => panic!("revoked stream must reset"),
            }
        }
    })
    .await
    .unwrap();
    assert_eq!(reason, Some(h2::Reason::CANCEL));
    slots(&fixture).await;
    assert_eq!(fixture.budget.used(), 0);
    assert!(!fixture.backend.actor().stopped());
    drop(permits);
    let peer = h2_response(&mut client, "/").await.await.unwrap();
    assert_eq!(receive(peer.into_body()).await, b"fixture");
    drop(slow);
    drop(client);
    fixture.cleanup().await;
    server.stop().await;
    let _ = tokio::time::timeout(Duration::from_secs(2), connection)
        .await
        .unwrap();
}
#[tokio::test]
async fn actual_http1_mjpeg_shared_quota_rejects_before_native_subscription_and_cleans_up() {
    let fixture = Fixture::new_mjpeg(JPEG);
    fixture.initialize().await;
    let server = Server::start(&fixture, false).await;
    let permits: Vec<_> = (0..64)
        .map(|_| {
            fixture
                .runtime
                .socket_slots
                .clone()
                .try_acquire_owned()
                .unwrap()
        })
        .collect();
    let mut stream = TcpStream::connect(server.address).await.unwrap();
    stream
        .write_all(
            b"GET /api/stream/mjpeg HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
        )
        .await
        .unwrap();
    let mut data = Vec::new();
    tokio::time::timeout(Duration::from_secs(2), stream.read_to_end(&mut data))
        .await
        .unwrap()
        .unwrap();
    assert!(data.starts_with(b"HTTP/1.1 503"));
    assert!(fixture.runtime.mjpeg.lock().session.is_none());
    assert!(!fixture.trace().contains("mjpeg:"));
    drop(permits);
    fixture.cleanup().await;
    server.stop().await;
}

async fn tls_stream(address: SocketAddr) -> tokio_rustls::client::TlsStream<TcpStream> {
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
    config.alpn_protocols = vec![b"h2".to_vec()];
    let stream = tokio_rustls::TlsConnector::from(Arc::new(config))
        .connect(
            rustls::pki_types::ServerName::try_from("localhost").unwrap(),
            TcpStream::connect(address).await.unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(stream.get_ref().1.alpn_protocol(), Some(b"h2".as_slice()));
    stream
}
async fn authenticated_fixture() -> Fixture {
    let mut fixture = Fixture::new_mjpeg(JPEG);
    let runtime = Arc::get_mut(&mut fixture.runtime).unwrap();
    runtime.config.authentication = "enable".into();
    runtime.config.security.trusted_proxies.clear();
    let copy = fixture.runtime.clone();
    tokio::task::spawn_blocking(move || {
        copy.store
            .create("operator", "test-password-strong", "admin")
            .unwrap();
        copy.store
            .create("viewer", "test-password-strong", "user")
            .unwrap();
    })
    .await
    .unwrap();
    fixture.initialize().await;
    fixture
}
fn token(fixture: &Fixture, username: &str, ttl: u64) -> String {
    let user = fixture.runtime.store.get(username).unwrap();
    let now = SystemTime::now()
        .duration_since(SystemTime::UNIX_EPOCH)
        .unwrap()
        .as_secs();
    crate::crypto::sign(
        &crate::crypto::Claims {
            username: username.into(),
            sub: username.into(),
            token_version: user.token_version,
            exp: now + ttl,
            iat: Some(now),
            nbf: None,
        },
        &fixture.runtime.config.jwt.secret_key,
    )
    .unwrap()
}
async fn rejected(address: SocketAddr, token: &str, code: u16) -> Vec<u8> {
    let mut stream = TcpStream::connect(address).await.unwrap();
    stream.write_all(format!("GET /api/stream/mjpeg HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nAuthorization: Bearer {token}\r\n\r\n").as_bytes()).await.unwrap();
    let mut data = Vec::new();
    tokio::time::timeout(Duration::from_secs(3), stream.read_to_end(&mut data))
        .await
        .unwrap()
        .unwrap();
    assert!(
        data.starts_with(format!("HTTP/1.1 {code}").as_bytes()),
        "{}",
        String::from_utf8_lossy(&data)
    );
    data
}
#[tokio::test]
async fn actual_http1_mjpeg_auth_factory_and_role_gates() {
    let fixture = authenticated_fixture().await;
    let server = Server::start(&fixture, false).await;
    for denied in ["".into(), "invalid".into(), token(&fixture, "viewer", 0)] {
        rejected(server.address, &denied, 401).await;
    }
    let factory = rejected(server.address, &token(&fixture, "admin", 120), 403).await;
    assert!(String::from_utf8_lossy(&factory).contains("password change required"));
    assert!(!fixture.trace().contains("mjpeg:"));
    assert_eq!(fixture.runtime.socket_slots.available_permits(), 64);
    for username in ["operator", "viewer"] {
        let mut reader = Reader::with_token(server.address, &token(&fixture, username, 120)).await;
        assert!(same_image(&reader.frame().await, JPEG));
        drop(reader);
        slots(&fixture).await;
    }
    assert_eq!(fixture.budget.used(), 0);
    fixture.cleanup().await;
    server.stop().await;
}
#[tokio::test]
async fn actual_http1_mjpeg_absolute_expiry_under_busy_jobs_preserves_hid_and_native_owner() {
    let fixture = authenticated_fixture().await;
    let server = Server::start(&fixture, false).await;
    let (input, input_status) = fixture.runtime.input.join().unwrap();
    let mut reader = Reader::with_token(server.address, &token(&fixture, "viewer", 4)).await;
    assert!(same_image(&reader.frame().await, JPEG));
    let permits: Vec<_> = (0..4)
        .map(|_| fixture.runtime.jobs.clone().try_acquire_owned().unwrap())
        .collect();
    slots(&fixture).await;
    assert!(input_status.borrow().enabled);
    assert_eq!(fixture.budget.used(), 0);
    assert!(!fixture.backend.actor().stopped());
    drop(permits);
    drop(reader);
    fixture.runtime.input.leave(input).unwrap();
    fixture.cleanup().await;
    server.stop().await;
}
#[tokio::test]
#[ignore = "owned loopback browser fixture; requires NK_MJPEG_BROWSER_STATE and an explicit stop marker"]
async fn browser_native_mjpeg_fixture() {
    let state = std::path::PathBuf::from(
        std::env::var_os("NK_MJPEG_BROWSER_STATE").expect("state directory"),
    );
    assert!(state.is_dir());
    assert!(!state.join("ready.json").exists());
    let fixture = Fixture::new_mjpeg(JPEG);
    std::fs::write(fixture.root.path().join("web/index.html"), r#"<!doctype html><html lang="ru"><meta charset="utf-8"><title>NanoKVM MJPEG transport fixture</title>
<style>body{font:18px sans-serif;background:#18202b;color:#eef;padding:32px}img{width:320px;image-rendering:pixelated;border:2px solid #eee}pre{white-space:pre-wrap}button{padding:12px;margin:16px 8px 16px 0}</style>
<h1>NanoKVM: MJPEG через Rust/C</h1><p>Изолированный синтетический C-захват, статичный кадр 64 × 48.</p>
<div id="viewers"><img id="first" alt="Первый зритель" src="/api/stream/mjpeg"></div>
<button id="add">Добавить зрителя</button><button id="close">Отключить зрителей</button><pre id="result">Ожидание первого кадра</pre>
<script>const results={};const starts={first:performance.now()};
function probe(){for(const image of document.querySelectorAll('img')){if(results[image.id]||!image.naturalWidth)continue;try{const canvas=document.createElement('canvas');canvas.width=image.naturalWidth;canvas.height=image.naturalHeight;const context=canvas.getContext('2d');context.drawImage(image,0,0);const pixel=Array.from(context.getImageData(32,24,1,1).data);results[image.id]={width:image.naturalWidth,height:image.naturalHeight,firstMs:performance.now()-starts[image.id],pixel};}catch(e){}}document.querySelector('#result').textContent=JSON.stringify(results,null,2);requestAnimationFrame(probe);}requestAnimationFrame(probe);
document.querySelector('#add').onclick=()=>{if(document.querySelector('#second'))return;const image=document.createElement('img');image.id='second';image.alt='Новый зритель статичного кадра';starts.second=performance.now();image.src='/api/stream/mjpeg';document.querySelector('#viewers').append(image);};
document.querySelector('#close').onclick=()=>{for(const image of document.querySelectorAll('img')){image.removeAttribute('src');image.remove();}document.querySelector('#close').disabled=true;};</script></html>"#).unwrap();
    fixture.initialize().await;
    let server = Server::start(&fixture, false).await;
    std::fs::write(state.join("ready.json"), serde_json::to_vec(&serde_json::json!({"url":format!("http://{}/",server.address),"pid":std::process::id(),"native":"synthetic C","timeout_seconds":180})).unwrap()).unwrap();
    let start = Instant::now();
    while !state.join("stop").exists() && start.elapsed() < Duration::from_secs(180) {
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    let viewers = fixture.runtime.mjpeg.lock().entries.len();
    fixture.cleanup().await;
    server.stop().await;
    assert_eq!(fixture.runtime.socket_slots.available_permits(), 64);
    assert_eq!(fixture.budget.used(), 0);
    std::fs::write(state.join("finished.json"), serde_json::to_vec(&serde_json::json!({"stop_marker":state.join("stop").exists(),"viewers_before_shutdown":viewers,"slots":64,"budget_used":0,"native_finished":fixture.backend.actor().finished()})).unwrap()).unwrap();
    assert!(
        state.join("stop").exists(),
        "browser fixture expired without explicit stop"
    );
}
