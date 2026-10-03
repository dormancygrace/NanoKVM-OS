// NanoKVM OS adaptation, 2026-10-03. SPDX-License-Identifier: AGPL-3.0-only
mod config;
mod framing;
mod identity;
mod input;
mod onekvm;
mod protocol;
mod rendezvous;
mod server;
mod status;
mod temporary_password;
mod upstream;

use std::{env, path::PathBuf, process::ExitCode};

use config::{Config, PasswordMode};

const DEFAULT_CONFIG_PATH: &str = "/etc/nanokvm-rustdesk/config.json";

#[tokio::main(flavor = "current_thread")]
async fn main() -> ExitCode {
    match run().await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("nanokvm-rustdesk: {error}");
            ExitCode::FAILURE
        }
    }
}

async fn run() -> Result<(), Box<dyn std::error::Error>> {
    let mut health = false;
    let mut check = false;
    let mut config_path = env::var_os("NANOKVM_RUSTDESK_CONFIG")
        .map(PathBuf::from)
        .map(|path| path.join("config.json"))
        .unwrap_or_else(|| PathBuf::from(DEFAULT_CONFIG_PATH));
    let mut arguments = env::args().skip(1);
    while let Some(argument) = arguments.next() {
        match argument.as_str() {
            "--version" => {
                println!(
                    "nanokvm-rustdesk {} (RustDesk protocol base {})",
                    env!("CARGO_PKG_VERSION"),
                    upstream::version()
                );
                return Ok(());
            }
            "--health" => health = true,
            "--check" => check = true,
            "--config" => {
                config_path = arguments.next().ok_or("--config requires a path")?.into();
            }
            _ => return Err(format!("unexpected argument {argument:?}").into()),
        }
    }

    if health {
        let status: serde_json::Value =
            serde_json::from_slice(&std::fs::read("/run/nanokvm-rustdesk/status.json")?)?;
        if status["pid"]
            .as_u64()
            .is_some_and(|pid| std::path::Path::new(&format!("/proc/{pid}")).exists())
        {
            return Ok(());
        }
        return Err("RustDesk daemon is not running".into());
    }

    let mut config = Config::load(&config_path)?;
    config.apply_server_mode();
    config.validate()?;
    if check {
        return Ok(());
    }
    if !config.service_enabled {
        return Err("service is disabled in configuration".into());
    }

    // Preserve a configured permanent credential while authenticating with
    // a fresh, in-memory password for each daemon run in temporary mode.
    let _password_guard = if config.password_mode == PasswordMode::Temporary {
        config.password = temporary_password::generate()?;
        Some(temporary_password::Guard::publish(
            std::path::Path::new(temporary_password::PATH),
            &config.password,
        )?)
    } else {
        match std::fs::remove_file(temporary_password::PATH) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
        None
    };

    let onekvm_identity = onekvm::Identity::load()?;
    let rustdesk_identity = identity::RustDeskIdentity::load()?;
    rustdesk_identity.publish_settings_output()?;
    server::run(config, onekvm_identity, rustdesk_identity).await?;
    Ok(())
}
