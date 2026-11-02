FROM maven:3.9.11-eclipse-temurin-17@sha256:e4a7ace3dc0d645ed97f8d9ad0b0d3f0b14fa8d150138f27f116d7105a639b82 AS build
WORKDIR /workspace
COPY pom.xml .
COPY mvnw .
COPY .mvn .mvn
COPY src src
# The base image sets MAVEN_CONFIG to a directory; this wrapper treats it as CLI arguments.
RUN chmod +x mvnw && MAVEN_CONFIG= ./mvnw --batch-mode --no-transfer-progress -DskipTests package \
    && cp target/spring-petclinic-rest-*.jar /application.jar

FROM eclipse-temurin:17-jre@sha256:4faa9eaf440514dd4f7ef71fecb92526e13679aea039bed5d2fc98e885c60dee
WORKDIR /app
COPY --from=build --chown=10001:10001 /application.jar application.jar
USER 10001:10001
EXPOSE 9966
ENTRYPOINT ["java", "-XX:MaxRAMPercentage=75.0", "-jar", "/app/application.jar"]
