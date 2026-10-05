//! Media sessions have their own registry namespace and never join the HID hub.
use crate::{api, sessions::Principal, Runtime};
use axum::{
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
    Json,
};
use serde_json::json;
use std::{sync::Arc, time::Duration};
use tokio::sync::watch;

pub(crate) async fn authenticate(
    runtime: &Arc<Runtime>,
    headers: &HeaderMap,
) -> Result<Principal, Box<Response>> {
    let Ok(permit) = runtime.jobs.clone().try_acquire_owned() else {
        return Err(Box::new(StatusCode::SERVICE_UNAVAILABLE.into_response()));
    };
    let copy = runtime.clone();
    let headers = headers.clone();
    let Ok(Ok(principal)) = tokio::task::spawn_blocking(move || {
        let _permit = permit;
        api::principal(&copy, &headers)
    })
    .await
    else {
        return Err(Box::new(api::unauthorized()));
    };
    if principal.user.must_change_password {
        return Err(Box::new(
            (
                StatusCode::FORBIDDEN,
                Json(json!({"code":-10,"msg":"password change required"})),
            )
                .into_response(),
        ));
    }
    Ok(principal)
}
pub(crate) async fn cancelled(receiver: &mut watch::Receiver<bool>) {
    loop {
        if *receiver.borrow_and_update() {
            return;
        }
        if receiver.changed().await.is_err() {
            return;
        }
    }
}
pub(crate) fn expiry_delay(principal: &Principal) -> Duration {
    match principal.expires {
        None => Duration::from_secs(3600),
        Some(exp) => std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|now| {
                Duration::from_secs(exp)
                    .saturating_sub(now)
                    .min(Duration::from_secs(3600))
            })
            .unwrap_or(Duration::ZERO),
    }
}
pub(crate) struct Registration {
    runtime: Arc<Runtime>,
    id: u64,
}
impl Registration {
    pub(crate) fn new(
        runtime: &Arc<Runtime>,
        principal: &Principal,
    ) -> Result<(Self, watch::Receiver<bool>), crate::Error> {
        let (id, receiver) = runtime.sessions.register_media(&principal.user.username)?;
        Ok((
            Self {
                runtime: runtime.clone(),
                id,
            },
            receiver,
        ))
    }
}
impl Drop for Registration {
    fn drop(&mut self) {
        self.runtime.sessions.remove_media(self.id);
    }
}

/// Long media/temporary-control requests must not expire a disabled-auth session
/// or mistake the one-hour reevaluation interval for the actual JWT expiry.
pub(crate) async fn expired(principal: &Principal) {
    if principal.expires.is_none() {
        std::future::pending::<()>().await;
        return;
    }
    loop {
        let remaining = expiry_delay(principal);
        if remaining.is_zero() {
            return;
        }
        tokio::time::sleep(remaining).await;
    }
}
