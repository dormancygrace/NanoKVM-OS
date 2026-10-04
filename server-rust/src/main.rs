use nanokvm_server::{app, Error, Runtime};
use std::{
    net::{IpAddr, SocketAddr},
    path::PathBuf,
    time::Duration,
};

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<(), Error> {
    let mut args = std::env::args().skip(1);
    let mut root = PathBuf::from("/");
    let mut web = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--root" => root = PathBuf::from(args.next().ok_or("--root requires a directory")?),
            "--web" => {
                web = Some(PathBuf::from(
                    args.next().ok_or("--web requires a directory")?,
                ))
            }
            "--version" => {
                println!(
                    "NanoKVM-Server {} (Rust; parity incomplete)",
                    env!("CARGO_PKG_VERSION")
                );
                return Ok(());
            }
            _ => return Err(format!("unknown argument: {arg}").into()),
        }
    }
    let root = root.canonicalize()?;
    if root == std::path::Path::new("/") {
        return Err("v3 production activation blocked: functional parity is incomplete; use an isolated --root".into());
    }
    let state = Runtime::load(&root)?;
    let host: IpAddr = if state.config.host.is_empty() {
        "127.0.0.1".parse()?
    } else {
        state.config.host.parse()?
    };
    if !host.is_loopback() {
        return Err("incomplete v3 runtime requires a loopback listener".into());
    }
    let web = web
        .unwrap_or(
            std::env::current_exe()?
                .parent()
                .ok_or("missing binary directory")?
                .join("web"),
        )
        .canonicalize()?;
    let router = app(state.clone(), web);
    let handle = axum_server::Handle::new();
    let shutdown = handle.clone();
    tokio::spawn(async move {
        let mut term = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            .expect("SIGTERM handler");
        tokio::select! {_ = tokio::signal::ctrl_c()=>{},_ = term.recv()=>{}}
        shutdown.graceful_shutdown(Some(Duration::from_secs(5)));
    });
    if state.config.proto == "https" {
        rustls::crypto::ring::default_provider()
            .install_default()
            .map_err(|_| "TLS provider already initialized")?;
        let crt = nanokvm_server::config::rooted(&root, &state.config.cert.crt)?;
        let key = nanokvm_server::config::rooted(&root, &state.config.cert.key)?;
        let tls = axum_server::tls_rustls::RustlsConfig::from_pem_file(crt, key).await?;
        let redirect_handle = handle.clone();
        let stop_both = handle.clone();
        let http_address = SocketAddr::new(host, state.config.port.http);
        // Bind both ports before starting either listener. Startup errors fail closed.
        let http_listener = std::net::TcpListener::bind(http_address)?;
        let https_listener =
            std::net::TcpListener::bind(SocketAddr::new(host, state.config.port.https))?;
        http_listener.set_nonblocking(true)?;
        https_listener.set_nonblocking(true)?;
        let redirects = nanokvm_server::redirect::router(state.config.port.https);
        let redirect_server = axum_server::from_tcp(http_listener)?
            .handle(redirect_handle)
            .serve(redirects.into_make_service());
        let address = SocketAddr::new(host, state.config.port.https);
        eprintln!("v3 isolated HTTPS listener {address}; functional parity incomplete");
        let tls_server = axum_server::from_tcp_rustls(https_listener, tls)?
            .handle(handle)
            .serve(router.into_make_service_with_connect_info::<SocketAddr>());
        tokio::pin!(redirect_server, tls_server);
        // Propagate errors from either listener and stop its companion.
        tokio::select! {
            result = &mut tls_server => {
                stop_both.shutdown();
                result?;
                redirect_server.await?;
            }
            result = &mut redirect_server => {
                stop_both.shutdown();
                result?;
                tls_server.await?;
            }
        }
    } else {
        let address = SocketAddr::new(host, state.config.port.http);
        eprintln!("v3 isolated HTTP listener {address}; functional parity incomplete");
        axum_server::bind(address)
            .handle(handle)
            .serve(router.into_make_service_with_connect_info::<SocketAddr>())
            .await?;
    }
    Ok(())
}
