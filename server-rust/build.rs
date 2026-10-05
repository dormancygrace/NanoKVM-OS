fn main() {
    println!("cargo:rerun-if-changed=native/capture");
    if std::env::var_os("CARGO_FEATURE_NATIVE_FIXTURE").is_none() {
        return;
    }
    // This optional test-only archive contains synthetic ABI implementations.
    // The release server never links the vendor or the fixture library.
    let mut build = cc::Build::new();
    build
        .files([
            "native/capture/nk-capture-worker.c",
            "native/capture/nk-capture-fixture.c",
        ])
        .include("native/capture")
        .std("c11")
        .opt_level(2)
        .warnings(true)
        .extra_warnings(true)
        .warnings_into_errors(true)
        .flag("-fstack-protector-strong");
    if std::env::var("TARGET")
        .expect("Cargo target")
        .starts_with("riscv64")
    {
        build
            .flag("-march=rv64gc")
            .flag("-mabi=lp64d")
            .flag("-fno-tree-vectorize")
            .flag("-fno-tree-slp-vectorize");
    }
    build.compile("nkcapturefixture");
}
