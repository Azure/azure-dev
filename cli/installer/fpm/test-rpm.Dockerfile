ARG prefix=''
ARG base='centos:8'
FROM ${prefix}${base}

WORKDIR /work
COPY test-rpm.sh /work/
COPY *.rpm /work/

# fix for https://github.com/Azure/azure-dev/issues/4076
RUN sed -i 's/mirrorlist/#mirrorlist/g' /etc/yum.repos.d/CentOS-*
RUN sed -i 's|#baseurl=http://mirror.centos.org|baseurl=http://vault.centos.org|g' /etc/yum.repos.d/CentOS-*

ENTRYPOINT [ "/bin/bash", "test-rpm.sh" ]
