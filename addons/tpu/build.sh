#!/bin/bash
# Build the TPU add-on: the CV181x runtime (dma-buf port), nanokvm-ocr and
# the OCR models, staged as /usr/lib/nanokvm-tpu.
#   addons/tpu/build.sh PLATFORM_OUTPUT MODELS OUTPUT
# PLATFORM_OUTPUT is a platform/build.sh output with the toolchain step done
# (its riscv64 musl cross compiler and sysroot); MODELS is the output of
# models/convert.sh. Writes OUTPUT/stage/usr/lib/nanokvm-tpu.
set -euo pipefail
platform=$(realpath "${1:?usage: build.sh PLATFORM_OUTPUT MODELS OUTPUT}")
models=$(realpath "${2:?models directory}")
out=$(realpath -m "${3:?output directory}")
here=$(dirname "$(realpath "$0")")
command -v cmake >/dev/null && command -v ninja >/dev/null || { echo 'cmake and ninja are needed' >&2; exit 1; }
export CMAKE_POLICY_VERSION_MINIMUM=3.5
bo=$platform/buildroot-output
cross=$bo/host/bin/riscv64-buildroot-linux-musl-
sysroot=$("${cross}gcc" -print-sysroot)
src=$out/src build=$out/build inst=$out/install
stage=$out/stage/usr/lib/nanokvm-tpu
rm -rf "$out"
mkdir -p "$src" "$build" "$inst" "$stage/bin" "$stage/lib" "$stage/models"

# The runtime: Sophgo's cviruntime as ported to dma-buf heaps, pinned.
repo=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["runtime"]["repository"])' "$here/upstream.json")
commit=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["runtime"]["commit"])' "$here/upstream.json")
git -C "$src" init -q
git -C "$src" remote add origin "$repo"
git -C "$src" sparse-checkout set cviruntime cvikernel cvimath flatbuffers cvibuilder cnpy
git -C "$src" fetch -q --depth 1 --filter=blob:none origin "$commit"
git -C "$src" checkout -q FETCH_HEAD
for patch in "$here"/patches/*.patch; do git -C "$src" apply "$patch"; done

# The platform's userspace flags (XTheadVector among them: nanokvm-ocr needs it).
isa=$(python3 "$here/../../scripts/nanokvm_cpu_profile.py" userspace)
cat > "$build/toolchain.cmake" <<EOF
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR riscv64)
set(CMAKE_C_COMPILER ${cross}gcc)
set(CMAKE_CXX_COMPILER ${cross}g++)
set(CMAKE_SYSROOT $sysroot)
set(CMAKE_FIND_ROOT_PATH $sysroot $inst)
set(CMAKE_FIND_ROOT_PATH_MODE_PROGRAM NEVER)
set(CMAKE_FIND_ROOT_PATH_MODE_LIBRARY ONLY)
set(CMAKE_FIND_ROOT_PATH_MODE_INCLUDE ONLY)
set(CMAKE_C_FLAGS "\${CMAKE_C_FLAGS} $isa" CACHE STRING "")
set(CMAKE_CXX_FLAGS "\${CMAKE_CXX_FLAGS} $isa" CACHE STRING "")
EOF
release=(-G Ninja -DCMAKE_BUILD_TYPE=RELEASE -DCMAKE_C_FLAGS_RELEASE=-O2 -DCMAKE_CXX_FLAGS_RELEASE=-O2)
target=("${release[@]}" -DCMAKE_TOOLCHAIN_FILE="$build/toolchain.cmake" -DCMAKE_INSTALL_PREFIX="$inst")
# Quiet steps; a failing one prints the end of its output.
run() { "$@" > "$build/step.log" 2>&1 || { tail -40 "$build/step.log" >&2; exit 1; }; }
# Host flatc and the cvimodel schema, then the target libraries.
run cmake -S "$src/flatbuffers" -B "$build/flatc" -G Ninja -DCMAKE_INSTALL_PREFIX="$build/flatc-install" -DFLATBUFFERS_BUILD_TESTS=OFF
run cmake --build "$build/flatc" --target install
run cmake -S "$src/cvibuilder" -B "$build/cvibuilder" -G Ninja -DFLATBUFFERS_PATH="$build/flatc-install" \
    -DCMAKE_INSTALL_PREFIX="$build/cvibuilder-install"
run cmake --build "$build/cvibuilder" --target install
mkdir -p "$inst/flatbuffers/include"
cp -r "$src/flatbuffers/include/flatbuffers" "$inst/flatbuffers/include/"
run cmake -S "$src/cvikernel" -B "$build/cvikernel" "${target[@]}" -DCHIP=cv181x
run cmake --build "$build/cvikernel" --target install
# cnpy and the runtime tools use zlib: the platform's zlib-ng (tools step).
zlib=$platform/tools/compress
export CPATH=$zlib/include LDFLAGS=-L$zlib/lib
run cmake -S "$src/cnpy" -B "$build/cnpy" "${target[@]}" \
    -DCMAKE_POSITION_INDEPENDENT_CODE=ON -DZLIB_INCLUDE_DIR="$zlib/include" -DZLIB_LIBRARY="$zlib/lib/libz.a"
run cmake --build "$build/cnpy" --target install
run cmake -S "$src/cviruntime" -B "$build/cviruntime" "${target[@]}" -DCHIP=cv181x -DRUNTIME=SOC -DENABLE_TEST=OFF \
    -DCVIKERNEL_PATH="$inst" -DCNPY_PATH="$inst/lib" -DFLATBUFFERS_PATH="$inst/flatbuffers" \
    -DCVIBUILDER_PATH="$build/cvibuilder-install"
run cmake --build "$build/cviruntime" --target install

# nanokvm-ocr, against the runtime just built; libraries next to it.
"${cross}g++" -std=gnu++17 -O2 -Wall -Wextra -pthread $isa -I"$inst/include" "$here/ocr/nanokvm-ocr.cpp" \
    -L"$inst/lib" -lcviruntime -Wl,-rpath,'$ORIGIN/../lib' -o "$stage/bin/nanokvm-ocr"
"${cross}strip" "$stage/bin/nanokvm-ocr"
cp "$inst/bin/model_runner" "$stage/bin/"
for lib in libcviruntime.so libcvikernel.so libcnpy.so; do
    cp -L "$inst/lib/$lib" "$stage/lib/"
    "${cross}strip" --strip-unneeded "$stage/lib/$lib"
done
# The C++ runtime of the toolchain that built them: the system's may be
# older. The rpath ($ORIGIN/../lib) makes these programs alone use it.
for lib in libstdc++.so.6 libgcc_s.so.1; do
    cp -L "$("${cross}g++" -print-file-name=$lib)" "$stage/lib/$lib"
    "${cross}strip" --strip-unneeded "$stage/lib/$lib"
done
(cd "$models" && sha256sum -c --quiet SHA256SUMS)
# The models go in with their command buffers already in the form the
# runtime gives them at load (ocr/cvimodel-dmabuf.c): a tool for this
# machine, with the runtime's converter.
soc=$src/cviruntime/src/soc
cc -O2 -Wall -I"$src/cviruntime/include" -I"$src/cvikernel/include" -I"$soc/181x" -I"$soc/common"     "$here/ocr/cvimodel-dmabuf.c" "$soc/181x/bm_dmabuf.c" -o "$build/cvimodel-dmabuf"
"$build/cvimodel-dmabuf" "$models/ocr-det.cvimodel" "$stage/models/ocr-det.cvimodel"
for lang in latin chinese cyrillic; do
    mkdir -p "$stage/models/$lang"
    for model in "$models/$lang"/ocr-*.cvimodel; do
        "$build/cvimodel-dmabuf" "$model" "$stage/models/$lang/${model##*/}"
    done
    cp "$models/$lang/ocr-dict.txt" "$stage/models/$lang/"
done
cp "$here/NOTICE" "$stage/"
du -sh "$stage"
