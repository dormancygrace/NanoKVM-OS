cmake_minimum_required(VERSION 3.25)
project(nanokvm_server CXX C)

# NanoKVM-Server in C++. Built for the SG2002 (one T-Head C906) with the Buildroot toolchain and the userspace
# profile of scripts/nanokvm_cpu_profile.py, passed in CMAKE_C_FLAGS/CMAKE_CXX_FLAGS by the build.
#
# Inputs, all pinned in platform/sources.lock:
#   LWS_ROOT     libwebsockets install (include/, lib/libwebsockets.a)
#   OPENSSL_ROOT nanokvm-openssl (include/, libssl.so, libcrypto.so)
#   GLAZE_ROOT   glaze source tree (include/)
#   RYML_HEADER  rapidyaml single header
#   KVM_ROOT     the native capture library: include/kvm_vision.h (support/additional/kvm) and dl_lib/libkvm.so
#                (built by the system build; installed next to the server as dl_lib/)
#   TURBOJPEG_ROOT libjpeg-turbo (static, scalar: its RVV code needs RVV 1.0), SPNG_SRC libspng source,
#                ZLIB_ROOT the image's zlib-ng (/usr/lib/libz.so.1 on the device)
#   LDC_ROOT     libdatachannel install with its libjuice, libsrtp2 and usrsctp static libraries (WebRTC)
#   CURL_INCLUDE, CURL_LIB  libcurl headers and the image's libcurl.so.4 (Alpine's, on nanokvm-openssl)

set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
# The runtime path is $ORIGIN/dl_lib alone (libkvm), as for the Go server; no build paths.
set(CMAKE_SKIP_BUILD_RPATH ON)

set(LWS_ROOT "" CACHE PATH "libwebsockets install prefix")
set(OPENSSL_ROOT "" CACHE PATH "OpenSSL build or install prefix")
set(GLAZE_ROOT "" CACHE PATH "glaze source tree")
set(RYML_HEADER "" CACHE FILEPATH "rapidyaml single header")
set(KVM_INCLUDE "" CACHE PATH "kvm_vision.h directory")
set(KVM_LIB_DIR "" CACHE PATH "libkvm.so directory")
set(TURBOJPEG_ROOT "" CACHE PATH "libjpeg-turbo install (include/turbojpeg.h, lib/libturbojpeg.a)")
set(SPNG_SRC "" CACHE PATH "libspng source directory (spng.c, spng.h)")
set(ZLIB_ROOT "" CACHE PATH "zlib-ng build (zlib.h, zconf.h, libz.so)")
set(CURL_INCLUDE "" CACHE PATH "libcurl headers (curl/curl.h)")
set(LDC_ROOT "" CACHE PATH "libdatachannel install (include/rtc, lib/*.a)")
set(CURL_LIB "" CACHE FILEPATH "the image's libcurl.so.4")

foreach(v LWS_ROOT OPENSSL_ROOT GLAZE_ROOT RYML_HEADER KVM_INCLUDE KVM_LIB_DIR TURBOJPEG_ROOT SPNG_SRC ZLIB_ROOT CURL_INCLUDE CURL_LIB LDC_ROOT)
  if(NOT ${v})
    message(FATAL_ERROR "${v} is not set")
  endif()
endforeach()

# The single header is included under a fixed name.
configure_file(${RYML_HEADER} ${CMAKE_BINARY_DIR}/third_party/ryml_all.hpp COPYONLY)

# The notification messages (the catalogues of the web interface's languages), embedded.
set(NOTIFY_MESSAGES_DIR ${CMAKE_SOURCE_DIR}/../server/service/notify/messages)
file(GLOB NOTIFY_MESSAGE_FILES ${NOTIFY_MESSAGES_DIR}/*.json)
add_custom_command(
  OUTPUT ${CMAKE_BINARY_DIR}/generated/notify_messages.inc
  COMMAND ${CMAKE_COMMAND} -DSRC=${NOTIFY_MESSAGES_DIR} -DOUT=${CMAKE_BINARY_DIR}/generated/notify_messages.inc
          -P ${CMAKE_SOURCE_DIR}/cmake/embed_messages.cmake
  DEPENDS ${NOTIFY_MESSAGE_FILES} ${CMAKE_SOURCE_DIR}/cmake/embed_messages.cmake
  VERBATIM)

add_executable(NanoKVM-Server
  src/main.cpp
  src/common/log.cpp
  src/common/loop.cpp
  src/common/process.cpp
  src/common/tz.cpp
  src/common/image.cpp
  src/common/http_client.cpp
  ${SPNG_SRC}/spng.c
  src/api/multipart.cpp
  src/api/request.cpp
  src/auth/brute_force.cpp
  src/auth/jwt.cpp
  src/auth/password.cpp
  src/auth/routes.cpp
  src/auth/session.cpp
  src/auth/store.cpp
  src/auth/totp.cpp
  src/config/yaml.cpp
  src/config/config.cpp
  src/http/server.cpp
  src/http/upload.cpp
  src/http/router.cpp
  src/http/static_files.cpp
  src/input/control.cpp
  src/input/hid.cpp
  src/input/jiggler.cpp
  src/input/keyboard_routes.cpp
  src/input/region.cpp
  src/input/ws.cpp
  src/system/atx.cpp
  src/system/branding.cpp
  src/system/failsafe.cpp
  src/system/gpio.cpp
  src/system/hardware.cpp
  src/system/bluetooth.cpp
  src/system/cpufreq.cpp
  src/system/dashboard.cpp
  src/system/device_settings.cpp
  src/system/info.cpp
  src/system/memory.cpp
  src/system/oled.cpp
  src/system/oled_state.cpp
  src/system/journal.cpp
  src/system/log_sanitize.cpp
  src/system/logs.cpp
  src/system/notify.cpp
  ${CMAKE_BINARY_DIR}/generated/notify_messages.inc
  src/system/live_status.cpp
  src/system/time_settings.cpp
  src/system/serial_terminal.cpp
  src/system/terminal.cpp
  src/system/scripts.cpp
  src/system/startpriority.cpp
  src/system/cron.cpp
  src/usb/composition.cpp
  src/usb/storage_reads.cpp
  src/video/capture_status.cpp
  src/video/capture_worker.c
  src/video/direct.cpp
  src/video/edid_profiles.cpp
  src/video/encoder.cpp
  src/video/frame.cpp
  src/video/hdmi.cpp
  src/video/mjpeg.cpp
  src/video/monitor.cpp
  src/video/parameter_sets.cpp
  src/video/routes.cpp
  src/video/screen.cpp
  src/video/settings.cpp
  src/video/setters.cpp
  src/video/source.cpp
  src/video/vision.cpp
  src/video/preview.cpp
  src/video/watermark.cpp
  src/video/webrtc.cpp
  src/video/audio.cpp
  src/video/rtc_common.cpp
)

target_include_directories(NanoKVM-Server PRIVATE
  src
  ${CMAKE_BINARY_DIR}/generated
  ${CMAKE_BINARY_DIR}/third_party
  ${LWS_ROOT}/include
  ${OPENSSL_ROOT}/include
  ${GLAZE_ROOT}/include
  ${KVM_INCLUDE}
  ${TURBOJPEG_ROOT}/include
  ${SPNG_SRC}
  ${ZLIB_ROOT}
  ${CURL_INCLUDE}
  ${LDC_ROOT}/include
)
# libspng is C, built with the server; it is quiet only without the C++ warnings.
set_source_files_properties(${SPNG_SRC}/spng.c PROPERTIES COMPILE_OPTIONS "-w")

target_compile_options(NanoKVM-Server PRIVATE -Wall -Wextra -Wno-missing-field-initializers)
target_compile_definitions(NanoKVM-Server PRIVATE RTC_STATIC)
# The C++ runtime of our GCC is newer than the one Alpine installs: link it into the server, and keep its symbols
# to the server, so that libkvm (C++ too) keeps the system's runtime. libkvm is found next to the server.
target_link_options(NanoKVM-Server PRIVATE -static-libstdc++ -static-libgcc -Wl,--exclude-libs,ALL
  "-Wl,-rpath,\$ORIGIN/dl_lib" -Wl,--enable-new-dtags -Wl,-rpath-link,${KVM_LIB_DIR}
  -Wl,--version-script,${CMAKE_CURRENT_SOURCE_DIR}/exports.map
  # libcurl's own dependencies (nghttp2, brotli, ...) are the image's; the build has only libcurl itself.
  -Wl,--allow-shlib-undefined)

find_library(LWS_LIB websockets PATHS ${LWS_ROOT}/lib NO_DEFAULT_PATH REQUIRED)
target_link_libraries(NanoKVM-Server PRIVATE
  ${LWS_LIB}
  ${OPENSSL_ROOT}/libssl.so
  ${OPENSSL_ROOT}/libcrypto.so
  ${KVM_LIB_DIR}/libkvm.so
  ${TURBOJPEG_ROOT}/lib/libturbojpeg.a
  ${ZLIB_ROOT}/libz.so
  ${CURL_LIB}
  ${LDC_ROOT}/lib/libdatachannel.a
  ${LDC_ROOT}/lib/libjuice.a
  ${LDC_ROOT}/lib/libsrtp2.a
  ${LDC_ROOT}/lib/libusrsctp.a
  pthread
)

install(TARGETS NanoKVM-Server RUNTIME DESTINATION .)
