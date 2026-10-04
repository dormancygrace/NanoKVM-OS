use axum::{
    extract::{Request, State},
    http::{header, StatusCode},
    response::{IntoResponse, Response},
    routing::any,
    Router,
};

pub fn router(port: u16) -> Router {
    Router::new().fallback(any(redirect)).with_state(port)
}
async fn redirect(State(port): State<u16>, req: Request) -> Response {
    let Some(raw) = req
        .headers()
        .get(header::HOST)
        .and_then(|v| v.to_str().ok())
    else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    let Ok(authority) = raw.parse::<axum::http::uri::Authority>() else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    let host = authority.host();
    if host.is_empty() || host.contains('@') {
        return StatusCode::BAD_REQUEST.into_response();
    }
    let host = if port == 443 {
        host.into()
    } else {
        format!("{host}:{port}")
    };
    let location = format!(
        "https://{host}{}",
        req.uri().path_and_query().map_or("/", |p| p.as_str())
    );
    (
        StatusCode::TEMPORARY_REDIRECT,
        [(header::LOCATION, location)],
    )
        .into_response()
}
#[cfg(test)]
mod tests {
    use super::*;
    use tower::ServiceExt;
    #[tokio::test]
    async fn https_redirect_preserves_query_and_ipv6() {
        for (host, expected) in [
            ("localhost:38080", "https://localhost:38443/path?x=1"),
            ("[::1]:38080", "https://[::1]:38443/path?x=1"),
        ] {
            let req = axum::http::Request::builder()
                .uri("/path?x=1")
                .header("host", host)
                .body(axum::body::Body::empty())
                .unwrap();
            let r = router(38443).oneshot(req).await.unwrap();
            assert_eq!(r.status(), StatusCode::TEMPORARY_REDIRECT);
            assert_eq!(r.headers()[header::LOCATION], expected);
        }
    }
}
