// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
use std::sync::OnceLock;

use serde::Deserialize;

#[derive(Deserialize)]
struct Upstream {
    rustdesk_version: String,
}

// The same file is embedded in the daemon and installed with its APK. The web
// application reads installed-package metadata rather than assuming its own base.
pub fn version() -> &'static str {
    static UPSTREAM: OnceLock<Upstream> = OnceLock::new();
    &UPSTREAM
        .get_or_init(|| {
            serde_json::from_str(include_str!("../upstream.json"))
                .expect("valid packaged upstream metadata")
        })
        .rustdesk_version
}
