FROM eclipse-temurin:17.0.17_10-jdk-jammy AS build
WORKDIR /workspace
COPY .mvn/ .mvn/
COPY mvnw pom.xml ./
RUN chmod +x mvnw && ./mvnw -B -ntp dependency:go-offline
COPY src/ src/
RUN ./mvnw -B -ntp -DskipTests package \
    && JAR="$(find target -maxdepth 1 -type f -name '*.jar' ! -name '*.original' -print -quit)" \
    && test -n "$JAR" \
    && cp "$JAR" /workspace/application.jar

FROM eclipse-temurin:17.0.17_10-jre-jammy
RUN groupadd --system spring && useradd --system --gid spring --home-dir /app spring
WORKDIR /app
COPY --from=build --chown=spring:spring /workspace/application.jar /app/application.jar
USER spring:spring
EXPOSE 8080
ENTRYPOINT ["java", "-jar", "/app/application.jar"]
