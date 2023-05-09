ARG prefix=''
ARG base='centos:7'
FROM ${prefix}${base}

WORKDIR /work
COPY test-rpm.sh /work/
COPY *.rpm /work/

ENTRYPOINT [ "/bin/bash", "test-rpm.sh" ]
