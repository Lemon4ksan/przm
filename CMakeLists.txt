# Copyright (c) 2026 Lemon4ksan All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

cmake_minimum_required(VERSION 3.15)
project(przm VERSION 0.5.0 LANGUAGES C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)

# Optimization flags
if(MSVC)
    add_compile_options(/O2 /W4)
else()
    add_compile_options(-O3 -Wall -Wextra -Wno-unused-parameter)
endif()

# Core static library
add_library(przm_dec STATIC
    src/przm_dec.c
)

target_include_directories(przm_dec PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
    $<INSTALL_INTERFACE:include>
)

# Test executable
add_executable(test_przm_dec
    tests/test_przm_dec.c
)

target_link_libraries(test_przm_dec PRIVATE przm_dec)

enable_testing()
add_test(NAME przm_dec_unit_tests COMMAND test_przm_dec)
