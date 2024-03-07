ARG prefix=''
ARG base='ubuntu:22.04'
FROM ${prefix}${base}

RUN apt update && \ 
    DEBIAN_FRONTEND=noninteractive apt install --no-install-recommends --no-install-suggests -y ruby binutils rpm && \
    gem install fpm

WORKDIR /work

ENTRYPOINT [ "fpm" ] 
