ARG prefix=''
ARG base='ubuntu:22.04'
FROM ${prefix}${base}

RUN apt update

WORKDIR /work

ENTRYPOINT [ "/bin/bash", "test-deb.sh" ]
