cmake_minimum_required(VERSION 3.25)

project(bridge CXX)
find_package(OneAuth CONFIG REQUIRED)
add_library(bridge SHARED)
file(GLOB source CONFIGURE_DEPENDS "*.cpp")
target_sources(bridge PRIVATE ${source})
target_compile_definitions(bridge PRIVATE UNICODE _UNICODE)
target_compile_features(bridge PRIVATE cxx_std_17)
target_link_libraries(bridge PRIVATE OneAuth::OneAuth)

add_custom_target(GenerateHashes ALL)
add_dependencies(GenerateHashes bridge)
foreach(DLL bridge.dll fmt.dll)
    add_custom_command(TARGET GenerateHashes POST_BUILD
                       WORKING_DIRECTORY $<TARGET_FILE_DIR:bridge>
                       COMMAND ${CMAKE_COMMAND} -E sha256sum ${DLL} > ${DLL}.sha256)
endforeach()
