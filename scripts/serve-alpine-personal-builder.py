#!/usr/bin/env python3
"""Small synchronous HTTP frontend for NanoKVM Alpine personal images."""

from __future__ import annotations

import argparse
import hashlib
from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import re
import subprocess
import threading
from urllib.error import URLError
from urllib.parse import unquote, urlsplit
from urllib.request import Request, urlopen


PACKAGE_RE = re.compile(r"^[a-z0-9][a-z0-9+_.-]{0,127}$")
BUILD_ID_RE = re.compile(r"^[0-9a-f]{24}$")
MAX_REQUEST_BYTES = 64 * 1024
MAX_PACKAGES = 256
MAX_INDEX_BYTES = 64 * 1024 * 1024
REQUIRED_OUTPUTS = {
    "alpine-rootfs.tar.gz",
    "boot-alpine.sd",
    "boot-alpine-recovery.sd",
    "recovery-manifest.json",
    "request-manifest.txt",
    "requested-packages.txt",
    "installed-packages.txt",
    "repositories",
    "apk-world.txt",
    "trusted-keys.sha256",
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def repository_index_sha256(repository: str) -> str:
    url = repository.rstrip("/") + "/riscv64/APKINDEX.tar.gz"
    request = Request(url, headers={"User-Agent": "NanoKVM-Alpine-Builder/1"})
    digest = hashlib.sha256()
    total = 0
    with urlopen(request, timeout=30) as response:
        while block := response.read(1024 * 1024):
            total += len(block)
            if total > MAX_INDEX_BYTES:
                raise ValueError(f"repository index exceeds {MAX_INDEX_BYTES} bytes: {url}")
            digest.update(block)
    return digest.hexdigest()


def resolve(root: Path, value: str) -> Path:
    path = Path(value)
    return path.resolve() if path.is_absolute() else (root / path).resolve()


def verified_output(output: Path) -> bool:
    sums = output / "SHA256SUMS"
    if not sums.is_file():
        return False
    try:
        names: set[str] = set()
        for line in sums.read_text(encoding="utf-8").splitlines():
            digest, name = line.split(None, 1)
            if not re.fullmatch(r"[0-9a-f]{64}", digest):
                return False
            name = name.strip()
            if name in names:
                return False
            names.add(name)
            candidate = (output / name).resolve()
            candidate.relative_to(output.resolve())
            if not candidate.is_file() or sha256(candidate) != digest:
                return False
        if names != REQUIRED_OUTPUTS:
            return False
    except (OSError, UnicodeDecodeError, ValueError):
        return False
    return True


class BuilderConfig:
    def __init__(self, source: Path) -> None:
        data = json.loads(source.read_text(encoding="utf-8"))
        self.public_origin = str(data.get("public_origin", "")).rstrip("/")
        if self.public_origin:
            parsed = urlsplit(self.public_origin)
            if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment:
                raise SystemExit("public_origin must be an HTTPS origin without credentials or a path")
        self.project_root = resolve(source.parent, data["project_root"])
        self.base_rootfs = resolve(self.project_root, data["base_rootfs"])
        self.boot_fit = resolve(self.project_root, data["boot_fit"])
        self.nanokvm_repo = resolve(self.project_root, data["nanokvm_repo"])
        self.tuned_repo = resolve(self.project_root, data["tuned_repo"])
        self.repo_keys = [resolve(self.project_root, item) for item in data["repo_keys"]]
        self.qemu_static = resolve(self.project_root, data["qemu_static"])
        self.output_root = resolve(self.project_root, data.get("output_root", "output"))
        self.alpine_version = str(data.get("alpine_version", "3.24"))
        self.alpine_main = str(data.get(
            "alpine_main",
            f"https://dl-cdn.alpinelinux.org/alpine/v{self.alpine_version}/main",
        ))
        self.alpine_community = str(data.get(
            "alpine_community",
            f"https://dl-cdn.alpinelinux.org/alpine/v{self.alpine_version}/community",
        ))
        self.builder = self.project_root / "scripts/build-alpine-personal-image.sh"
        self.update_builder = self.project_root / "scripts/build-alpine-update-bundle.sh"
        self.recovery_builder = self.project_root / "scripts/build-alpine-recovery-fit.py"
        self.recovery_init = self.project_root / "firmware/alpine/recovery/init"
        for label, path in (
            ("base_rootfs", self.base_rootfs),
            ("boot_fit", self.boot_fit),
            ("qemu_static", self.qemu_static),
            ("builder", self.builder),
            ("update_builder", self.update_builder),
            ("recovery_builder", self.recovery_builder),
            ("recovery_init", self.recovery_init),
        ):
            if not path.is_file():
                raise SystemExit(f"missing {label}: {path}")
        for label, path in (
            ("nanokvm_repo", self.nanokvm_repo),
            ("tuned_repo", self.tuned_repo),
        ):
            if not (path / "riscv64/APKINDEX.tar.gz").is_file():
                raise SystemExit(f"missing {label} APKINDEX: {path}")
        if not self.repo_keys:
            raise SystemExit("repo_keys must contain at least one public key")
        for key in self.repo_keys:
            text = key.read_text(encoding="ascii", errors="strict")
            if "PUBLIC KEY" not in text or "PRIVATE KEY" in text:
                raise SystemExit(f"not a public PEM key: {key}")
        self.base_sha256 = sha256(self.base_rootfs)
        self.boot_sha256 = sha256(self.boot_fit)
        if self.base_sha256 != data["base_sha256"]:
            raise SystemExit("base_rootfs SHA256 does not match the pinned config")
        if self.boot_sha256 != data["boot_sha256"]:
            raise SystemExit("boot_fit SHA256 does not match the pinned config")
        self.output_root.mkdir(parents=True, exist_ok=True)

    def build_id(self, profile: str, packages: list[str]) -> str:
        # Re-read every index for each request. This makes a repeated attended
        # request pick up newly published Alpine and NanoKVM APKs instead of
        # returning an image cached against only repository URLs.
        identity = {
            "profile": profile,
            "public_origin": self.public_origin,
            "packages": packages,
            "base_sha256": self.base_sha256,
            "boot_sha256": self.boot_sha256,
            "nanokvm_index_sha256": sha256(self.nanokvm_repo / "riscv64/APKINDEX.tar.gz"),
            "tuned_index_sha256": sha256(self.tuned_repo / "riscv64/APKINDEX.tar.gz") if profile == "c906-scalar" else None,
            "alpine_main_index_sha256": repository_index_sha256(self.alpine_main),
            "alpine_community_index_sha256": repository_index_sha256(self.alpine_community),
            "builder_sha256": sha256(self.builder),
            "update_builder_sha256": sha256(self.update_builder),
            "recovery_builder_sha256": sha256(self.recovery_builder),
            "recovery_init_sha256": sha256(self.recovery_init),
            "qemu_sha256": sha256(self.qemu_static),
            "repo_key_sha256": [sha256(path) for path in self.repo_keys],
            "alpine_version": self.alpine_version,
            "alpine_main": self.alpine_main,
            "alpine_community": self.alpine_community,
        }
        encoded = json.dumps(identity, sort_keys=True, separators=(",", ":")).encode()
        return hashlib.sha256(encoded).hexdigest()[:24]


class BuilderServer(ThreadingHTTPServer):
    daemon_threads = True

    def __init__(self, address: tuple[str, int], config: BuilderConfig) -> None:
        super().__init__(address, RequestHandler)
        self.config = config
        self.build_lock = threading.Lock()

    @property
    def local_url(self) -> str:
        return f"http://127.0.0.1:{self.server_port}"


class RequestHandler(BaseHTTPRequestHandler):
    server: BuilderServer

    def log_message(self, pattern: str, *args: object) -> None:
        print(f"{self.address_string()} - {pattern % args}")

    def json_response(self, status: HTTPStatus, payload: dict[str, object]) -> None:
        encoded = json.dumps(payload, sort_keys=True).encode("utf-8") + b"\n"
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def do_GET(self) -> None:  # noqa: N802
        path = urlsplit(self.path).path
        if path == "/v1/health":
            self.json_response(HTTPStatus.OK, {
                "status": "ok",
                "base_sha256": self.server.config.base_sha256,
                "boot_sha256": self.server.config.boot_sha256,
            })
            return
        if path.startswith("/v1/builds/"):
            build_id = path.removeprefix("/v1/builds/")
            if not BUILD_ID_RE.fullmatch(build_id):
                self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid build id"})
                return
            output = self.server.config.output_root / build_id
            if not output.exists():
                self.json_response(HTTPStatus.NOT_FOUND, {"error": "build not found"})
                return
            if not verified_output(output):
                self.json_response(HTTPStatus.CONFLICT, {"error": "build is incomplete or corrupt"})
                return
            self.json_response(HTTPStatus.OK, self.build_payload(build_id, output, cached=True))
            return
        for prefix, root in (
            ("/repos/nanokvm/", self.server.config.nanokvm_repo),
            ("/repos/c906-qualified/", self.server.config.tuned_repo),
        ):
            if path.startswith(prefix):
                self.serve_file(root, path.removeprefix(prefix))
                return
        if path.startswith("/artifacts/"):
            parts = path.removeprefix("/artifacts/").split("/", 1)
            if len(parts) != 2 or not BUILD_ID_RE.fullmatch(parts[0]):
                self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid artifact path"})
                return
            self.serve_file(self.server.config.output_root / parts[0], parts[1])
            return
        if path.startswith("/logs/"):
            build_id = path.removeprefix("/logs/")
            if not BUILD_ID_RE.fullmatch(build_id):
                self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid build id"})
                return
            self.serve_file(self.server.config.output_root, build_id + ".log")
            return
        self.json_response(HTTPStatus.NOT_FOUND, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if urlsplit(self.path).path != "/v1/builds":
            self.json_response(HTTPStatus.NOT_FOUND, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            length = -1
        if length < 1 or length > MAX_REQUEST_BYTES:
            self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid request size"})
            return
        try:
            request = json.loads(self.rfile.read(length))
        except (UnicodeDecodeError, json.JSONDecodeError):
            self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid JSON"})
            return
        if not isinstance(request, dict) or set(request) - {"profile", "packages"}:
            self.json_response(HTTPStatus.BAD_REQUEST, {"error": "unknown request field"})
            return
        profile = request.get("profile")
        packages = request.get("packages", [])
        if profile not in {"stock", "c906-scalar"}:
            self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid profile"})
            return
        if not isinstance(packages, list) or len(packages) > MAX_PACKAGES:
            self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid package list"})
            return
        if any(not isinstance(item, str) or not PACKAGE_RE.fullmatch(item) for item in packages):
            self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid package name"})
            return
        packages = sorted(set(packages))
        try:
            build_id = self.server.config.build_id(profile, packages)
        except (OSError, URLError, ValueError) as error:
            self.json_response(
                HTTPStatus.SERVICE_UNAVAILABLE,
                {"error": f"cannot refresh repository indexes: {error}"},
            )
            return
        output = self.server.config.output_root / build_id
        if verified_output(output):
            self.json_response(HTTPStatus.OK, self.build_payload(build_id, output, cached=True))
            return
        if output.exists():
            self.json_response(HTTPStatus.CONFLICT, {"error": "incomplete output exists"})
            return
        with self.server.build_lock:
            if verified_output(output):
                self.json_response(HTTPStatus.OK, self.build_payload(build_id, output, cached=True))
                return
            command = self.build_command(profile, packages, output)
            result = subprocess.run(
                command,
                cwd=self.server.config.project_root,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
            )
            log = self.server.config.output_root / f"{build_id}.log"
            log.write_text(result.stdout, encoding="utf-8")
            if result.returncode != 0 or not verified_output(output):
                self.json_response(HTTPStatus.INTERNAL_SERVER_ERROR, {
                    "error": "image build failed",
                    "build_id": build_id,
                    "log": f"/logs/{build_id}",
                })
                return
        self.json_response(HTTPStatus.CREATED, self.build_payload(build_id, output, cached=False))

    def build_command(self, profile: str, packages: list[str], output: Path) -> list[str]:
        config = self.server.config
        command = [
            str(config.builder),
            "--profile", profile,
            "--base-rootfs", str(config.base_rootfs),
            "--base-sha256", config.base_sha256,
            "--boot-fit", str(config.boot_fit),
            "--boot-sha256", config.boot_sha256,
            "--nanokvm-repo", self.server.local_url + "/repos/nanokvm",
            "--alpine-version", config.alpine_version,
            "--alpine-main", config.alpine_main,
            "--alpine-community", config.alpine_community,
            "--qemu-static", str(config.qemu_static),
            "--output", str(output),
        ]
        if profile == "c906-scalar":
            command.extend(["--tuned-repo", self.server.local_url + "/repos/c906-qualified"])
        if config.public_origin:
            command.extend(["--runtime-nanokvm-repo", config.public_origin + "/repos/nanokvm"])
            if profile == "c906-scalar":
                command.extend(["--runtime-tuned-repo", config.public_origin + "/repos/c906-qualified"])
        for key in config.repo_keys:
            command.extend(["--repo-key", str(key)])
        for package in packages:
            command.extend(["--package", package])
        return command

    def build_payload(self, build_id: str, output: Path, *, cached: bool) -> dict[str, object]:
        files = []
        for line in (output / "SHA256SUMS").read_text(encoding="utf-8").splitlines():
            digest, name = line.split(None, 1)
            name = name.strip()
            files.append({
                "name": name,
                "sha256": digest,
                "url": f"/artifacts/{build_id}/{name}",
            })
        return {"build_id": build_id, "cached": cached, "files": files}

    def serve_file(self, root: Path, relative: str) -> None:
        try:
            relative = unquote(relative)
            candidate = (root / relative).resolve()
            candidate.relative_to(root.resolve())
        except (ValueError, OSError):
            self.json_response(HTTPStatus.BAD_REQUEST, {"error": "invalid path"})
            return
        if not candidate.is_file():
            self.json_response(HTTPStatus.NOT_FOUND, {"error": "file not found"})
            return
        self.send_response(HTTPStatus.OK)
        self.send_header("Content-Length", str(candidate.stat().st_size))
        self.send_header("Content-Type", "application/octet-stream")
        self.end_headers()
        with candidate.open("rb") as stream:
            shutil_copyfileobj(stream, self.wfile)


def shutil_copyfileobj(source: object, destination: object, length: int = 1024 * 1024) -> None:
    while True:
        block = source.read(length)  # type: ignore[attr-defined]
        if not block:
            return
        destination.write(block)  # type: ignore[attr-defined]


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--listen", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8080)
    args = parser.parse_args()
    if hasattr(os, "geteuid") and os.geteuid() != 0:
        raise SystemExit("the builder service must run as root for chroot and mounts")
    config = BuilderConfig(args.config.resolve())
    server = BuilderServer((args.listen, args.port), config)
    print(f"NanoKVM Alpine builder listening on {args.listen}:{server.server_port}")
    server.serve_forever()


if __name__ == "__main__":
    main()
