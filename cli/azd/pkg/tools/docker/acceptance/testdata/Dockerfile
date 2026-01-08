FROM alpine:3.19 AS base
ARG BUILD_VERSION=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=secret,id=test_secret \
    echo "azd acceptance test image" > /info.txt && \
    echo "version: ${BUILD_VERSION}" >> /info.txt && \
    echo "date: ${BUILD_DATE}" >> /info.txt && \
    if [ -f /run/secrets/test_secret ]; then \
        echo "secret: present" >> /info.txt; \
    fi

FROM base AS final
CMD ["cat", "/info.txt"]
