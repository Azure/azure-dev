ARG prefix=''
ARG base='centos:7'
FROM ${prefix}${base}

WORKDIR /work

ENTRYPOINT [ "/bin/bash", "test-rpm.sh" ]
