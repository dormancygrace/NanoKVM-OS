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

set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

set(LWS_ROOT "" CACHE PATH "libwebsockets install prefix")
set(OPENSSL_ROOT "" CACHE PATH "OpenSSL build or install prefix")
set(GLAZE_ROOT "" CACHE PATH "glaze source tree")
set(RYML_HEADER "" CACHE FILEPATH "rapidyaml single header")

foreach(v LWS_ROOT OPENSSL_ROOT GLAZE_ROOT RYML_HEADER)
  if(NOT ${v})
    message(FATAL_ERROR "${v} is not set")
  endif()
endforeach()

# The single header is included under a fixed name.
configure_file(${RYML_HEADER} ${CMAKE_BINARY_DIR}/third_party/ryml_all.hpp COPYONLY)

add_executable(NanoKVM-Server
  src/main.cpp
  src/common/log.cpp
  src/common/loop.cpp
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
  src/http/router.cpp
  src/http/static_files.cpp
)

target_include_directories(NanoKVM-Server PRIVATE
  src
  ${CMAKE_BINARY_DIR}/third_party
  ${LWS_ROOT}/include
  ${OPENSSL_ROOT}/include
  ${GLAZE_ROOT}/include
)

target_compile_options(NanoKVM-Server PRIVATE -Wall -Wextra -Wno-missing-field-initializers)
# The C++ runtime of our GCC is newer than the one Alpine installs: link it into the server.
target_link_options(NanoKVM-Server PRIVATE -static-libstdc++ -static-libgcc)

find_library(LWS_LIB websockets PATHS ${LWS_ROOT}/lib NO_DEFAULT_PATH REQUIRED)
target_link_libraries(NanoKVM-Server PRIVATE
  ${LWS_LIB}
  ${OPENSSL_ROOT}/libssl.so
  ${OPENSSL_ROOT}/libcrypto.so
  pthread
)

install(TARGETS NanoKVM-Server RUNTIME DESTINATION .)
