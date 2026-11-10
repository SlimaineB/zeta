cmake_minimum_required(VERSION 3.20)
project(hello LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_executable(hello src/main.cpp)

install(TARGETS hello RUNTIME DESTINATION bin)
