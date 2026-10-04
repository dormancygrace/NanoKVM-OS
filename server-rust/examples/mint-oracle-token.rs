//! Test-only interoperability token for the isolated Go oracle; never packaged.
use nanokvm_server::{crypto, Error};
use serde_json::Value;
fn main() -> Result<(), Error> {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../docs/experiments/v3.0/auth-go-oracle.json"
    ))?;
    let viewer = fixture["database"]["users"]
        .as_array()
        .ok_or("fixture users missing")?
        .iter()
        .find(|u| u["username"] == "viewer")
        .ok_or("viewer fixture missing")?;
    let token = crypto::sign(
        &crypto::Claims {
            username: "viewer".into(),
            sub: "viewer".into(),
            token_version: viewer["tokenVersion"]
                .as_u64()
                .ok_or("fixture tokenVersion missing")?,
            exp: 5_000_000_000,
            iat: None,
            nbf: None,
        },
        "v3-test-only-secret",
    )?;
    println!("{token}");
    Ok(())
}
