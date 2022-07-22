# [Choice] Java version (use -bullseye variants on local arm64/Apple Silicon): 11, 17, 11-bullseye, 17-bullseye, 11-buster, 17-buster
 ARG VARIANT="11"
 FROM mcr.microsoft.com/vscode/devcontainers/java:0-${VARIANT}

 # [Option] Install Maven
 ARG INSTALL_MAVEN="true"
 ARG MAVEN_VERSION=""
 # [Option] Install Gradle
 ARG INSTALL_GRADLE="false"
 ARG GRADLE_VERSION=""
 RUN if [ "${INSTALL_MAVEN}" = "true" ]; then su vscode -c "umask 0002 && . /usr/local/sdkman/bin/sdkman-init.sh && sdk install maven \"${MAVEN_VERSION}\""; fi \
     && if [ "${INSTALL_GRADLE}" = "true" ]; then su vscode -c "umask 0002 && . /usr/local/sdkman/bin/sdkman-init.sh && sdk install gradle \"${GRADLE_VERSION}\""; fi
