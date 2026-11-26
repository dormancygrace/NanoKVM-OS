cmake_minimum_required(VERSION 3.25)
project(nanokvm_server CXX C ASM)

# NanoKVM-Server in C++. Built for the SG2002 (one T-Head C906) with the Buildroot toolchain and the server
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
#   LIVE555_SRC  live555 source tree (the RTSP server), built here as a static library

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
set(LIVE555_SRC "" CACHE PATH "live555 source tree (liveMedia/, groupsock/, UsageEnvironment/, BasicUsageEnvironment/)")

foreach(v LWS_ROOT OPENSSL_ROOT GLAZE_ROOT RYML_HEADER KVM_INCLUDE KVM_LIB_DIR TURBOJPEG_ROOT SPNG_SRC ZLIB_ROOT CURL_INCLUDE CURL_LIB LDC_ROOT LIVE555_SRC)
  if(NOT ${v})
    message(FATAL_ERROR "${v} is not set")
  endif()
endforeach()

# live555: the RTSP server of the video stream (src/video/rtsp.cpp). Without OpenSSL: no RTSP over TLS or SRTP.
set(LIVE555_DIRS liveMedia groupsock UsageEnvironment BasicUsageEnvironment)
set(LIVE555_SOURCES)
set(LIVE555_INCLUDES)
foreach(d ${LIVE555_DIRS})
  file(GLOB s ${LIVE555_SRC}/${d}/*.cpp ${LIVE555_SRC}/${d}/*.c)
  list(APPEND LIVE555_SOURCES ${s})
  list(APPEND LIVE555_INCLUDES ${LIVE555_SRC}/${d}/include)
endforeach()
add_library(live555 STATIC ${LIVE555_SOURCES})
target_include_directories(live555 PUBLIC ${LIVE555_INCLUDES})
target_compile_definitions(live555 PRIVATE SOCKLEN_T=socklen_t NO_OPENSSL=1 _LARGEFILE_SOURCE=1 _FILE_OFFSET_BITS=64 BSD=1)
target_compile_definitions(live555 PUBLIC NO_OPENSSL=1)
set_target_properties(live555 PROPERTIES CXX_STANDARD 20 POSITION_INDEPENDENT_CODE ON)
target_compile_options(live555 PRIVATE -w)

# The single header is included under a fixed name.
configure_file(${RYML_HEADER} ${CMAKE_BINARY_DIR}/third_party/ryml_all.hpp COPYONLY)

# The notification messages (the catalogues of the web interface's languages), embedded.
set(NOTIFY_MESSAGES_DIR ${CMAKE_SOURCE_DIR}/data/notify-messages)
file(GLOB NOTIFY_MESSAGE_FILES ${NOTIFY_MESSAGES_DIR}/*.json)
add_custom_command(
  OUTPUT ${CMAKE_BINARY_DIR}/generated/notify_messages.inc
  COMMAND ${CMAKE_COMMAND} -DSRC=${NOTIFY_MESSAGES_DIR} -DOUT=${CMAKE_BINARY_DIR}/generated/notify_messages.inc
          -P ${CMAKE_SOURCE_DIR}/cmake/embed_messages.cmake
  DEPENDS ${NOTIFY_MESSAGE_FILES} ${CMAKE_SOURCE_DIR}/cmake/embed_messages.cmake
  VERBATIM)

# The network family's udhcpc hooks and IPv6 boot script, embedded byte for byte as the Go server embeds them.
set(NETWORK_SCRIPTS_DIR ${CMAKE_SOURCE_DIR}/data/network-scripts)
add_custom_command(
  OUTPUT ${CMAKE_BINARY_DIR}/generated/network_scripts.inc
  COMMAND ${CMAKE_COMMAND} -DSRC=${NETWORK_SCRIPTS_DIR} -DOUT=${CMAKE_BINARY_DIR}/generated/network_scripts.inc
          -P ${CMAKE_SOURCE_DIR}/src/network/embed_scripts.cmake
  DEPENDS ${NETWORK_SCRIPTS_DIR}/99-nanokvm-dns ${NETWORK_SCRIPTS_DIR}/99-nanokvm-gateway
          ${NETWORK_SCRIPTS_DIR}/98-nanokvm-wifi-source ${NETWORK_SCRIPTS_DIR}/S02ipv6
          ${CMAKE_SOURCE_DIR}/src/network/embed_scripts.cmake
  VERBATIM)

add_executable(NanoKVM-Server
  src/main.cpp
  src/common/string_c906.S
  src/ai/json.cpp
  src/ai/files.cpp
  src/ai/keys.cpp
  src/ai/mcp.cpp
  src/ai/mcp_manage.cpp
  src/ai/capture.cpp
  src/ai/control_routes.cpp
  src/ai/picoclaw.cpp
  src/ai/picoclaw_config.cpp
  src/ai/picoclaw_sessions.cpp
  src/ai/picoclaw_install.cpp
  src/ai/routes.cpp
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
  src/config/config_tls.cpp
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
  src/input/attention.cpp
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
  src/system/log_mask.cpp
  src/system/diagnostics.cpp
  src/system/dev_archive.cpp
  src/system/logs.cpp
  src/system/notify.cpp
  src/notify/gostd.cpp
  src/notify/json.cpp
  src/notify/model.cpp
  src/notify/channels.cpp
  src/notify/oauth.cpp
  src/notify/transport.cpp
  src/notify/service.cpp
  src/notify/notify.cpp
  src/notify/host_watch.cpp
  ${CMAKE_BINARY_DIR}/generated/notify_messages.inc
  src/system/live_status.cpp
  src/system/time_settings.cpp
  src/system/serial_terminal.cpp
  src/system/serial_console.cpp
  src/system/terminal.cpp
  src/system/scripts.cpp
  src/system/startpriority.cpp
  src/system/cron.cpp
  src/osupdate/apkpkg.cpp
  src/osupdate/apkrun.cpp
  src/osupdate/godecode.cpp
  src/osupdate/helper.cpp
  src/osupdate/notice.cpp
  src/osupdate/osupdate.cpp
  src/osupdate/routes.cpp
  src/extensions/acme.cpp
  src/extensions/common.cpp
  src/extensions/netbird.cpp
  src/extensions/netsvc.cpp
  src/extensions/openvpn.cpp
  src/extensions/routes.cpp
  src/extensions/tailscale.cpp
  src/extensions/vpn.cpp
  src/extensions/wireguard.cpp
  src/extensions/zerotier.cpp
  src/osupdate/software.cpp
  src/recovery/archive.cpp
  src/recovery/engine.cpp
  src/recovery/filesystem.cpp
  src/recovery/routes.cpp
  src/recovery/yaml_v3.cpp
  src/system/cli.cpp
  src/system/tls_x509.cpp
  src/system/tls.cpp
  src/system/ssh_keys.cpp
  src/usb/composition.cpp
  src/usb/gadget.cpp
  src/usb/gojson_read.cpp
  src/usb/gotext.cpp
  src/usb/requests.cpp
  src/usb/remote_access.cpp
  src/usb/usbinternet.cpp
  src/usb/host_sensors.cpp
  src/usb/host_sensors_core.cpp
  src/usb/host_watchdog_core.cpp
  src/usb/helper_disk.cpp
  src/storage/download.cpp
  src/storage/gadget.cpp
  src/storage/go.cpp
  src/storage/http_source.cpp
  src/storage/images.cpp
  src/storage/netboot.cpp
  src/storage/remote.cpp
  src/storage/remotemedia.cpp
  src/storage/smb.cpp
  src/storage/userdisk.cpp
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
  src/video/rtsp.cpp
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
  src/video/srtp_frame.cpp
  src/video/dtls_seal.cpp
  src/video/screen_text.cpp
  src/video/screen_watch.cpp
  src/video/screen_watch_core.cpp
  src/video/videogate.cpp
  src/standby/core.cpp
  src/standby/standby.cpp
  src/bmc/config.cpp
  src/bmc/ipmi.cpp
  src/bmc/redfish.cpp
  src/bmc/service.cpp
  src/bmc/host_sensors_link.cpp
  src/rustdesk/service.cpp
  src/rustdesk/routes.cpp
  src/rustdesk/bridge.cpp
  src/rustdesk/bridge_socket.cpp
  src/rustdesk/bridge_wire.cpp
  src/rustdesk/webrtc_bridge.cpp
  src/network/util.cpp
  src/network/wifi.cpp
  src/network/wifi_ap.cpp
  src/network/trial.cpp
  src/network/ethernet.cpp
  src/network/dns.cpp
  src/network/ipv6.cpp
  src/network/gateway.cpp
  src/network/wol.cpp
  src/network/discovery.cpp
  src/network/webrtc_servers.cpp
  src/network/routes.cpp
  src/network/scripts.cpp
  ${CMAKE_BINARY_DIR}/generated/network_scripts.inc
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
  live555
  pthread
)

# The apk commit hooks' helper: applies a package transaction to the running services. On its own, without the
# server's libraries (it runs from apk while those may be replaced).
add_executable(nkos-apply-updates tools/nkos-apply-updates.cpp)
target_include_directories(nkos-apply-updates PRIVATE src ${GLAZE_ROOT}/include)
target_compile_options(nkos-apply-updates PRIVATE -Wall -Wextra -Wno-missing-field-initializers)
target_link_options(nkos-apply-updates PRIVATE -static-libstdc++ -static-libgcc)

# nkos-usb-internet: USB internet sharing for S30usbnet and its watcher (no libkvm, no lws).
add_executable(nkos-usb-internet tools/nkos-usb-internet.cpp src/usb/usbinternet.cpp src/usb/gojson_read.cpp
  src/usb/gotext.cpp src/config/yaml.cpp src/common/process.cpp)
target_include_directories(nkos-usb-internet PRIVATE src ${CMAKE_BINARY_DIR}/third_party)
target_compile_options(nkos-usb-internet PRIVATE -Wall -Wextra -Wno-missing-field-initializers)
target_link_options(nkos-usb-internet PRIVATE -static-libstdc++ -static-libgcc)

# nkos-update: the detached package worker and early-boot settings recovery (server/cmd/nkos-update), on the
# server's osupdate and recovery code.
add_executable(nkos-update
  src/nkos_update/main.cpp
  src/common/log.cpp
  src/common/process.cpp
  src/config/yaml.cpp
  src/osupdate/apkrun.cpp
  src/osupdate/godecode.cpp
  src/osupdate/helper.cpp
  src/osupdate/notice.cpp
  src/osupdate/osupdate.cpp
  src/osupdate/software.cpp
  src/recovery/archive.cpp
  src/recovery/engine.cpp
  src/recovery/filesystem.cpp
  src/recovery/yaml_v3.cpp
)
target_include_directories(nkos-update PRIVATE src ${CMAKE_BINARY_DIR}/third_party ${OPENSSL_ROOT}/include)
target_compile_options(nkos-update PRIVATE -Wall -Wextra -Wno-missing-field-initializers)
target_link_options(nkos-update PRIVATE -static-libstdc++ -static-libgcc)
target_link_libraries(nkos-update PRIVATE ${OPENSSL_ROOT}/libcrypto.so pthread)
# nkos-standby, the RAM helper of network standby (src/standby/helper.cpp). Fully static: the server's preparation
# refuses a helper with a program interpreter, and it runs from the RAM root after the SD card is released. OpenSSL
# for its HTTPS listener; nothing of the server's other libraries.
add_executable(nkos-standby src/standby/helper.cpp src/standby/core.cpp src/storage/go.cpp)
target_include_directories(nkos-standby PRIVATE src ${OPENSSL_ROOT}/include)
target_compile_options(nkos-standby PRIVATE -Wall -Wextra -Wno-missing-field-initializers)
target_link_options(nkos-standby PRIVATE -static)
target_link_libraries(nkos-standby PRIVATE ${OPENSSL_ROOT}/libssl.a ${OPENSSL_ROOT}/libcrypto.a pthread)

install(TARGETS NanoKVM-Server nkos-apply-updates nkos-usb-internet nkos-update nkos-standby RUNTIME DESTINATION .)
