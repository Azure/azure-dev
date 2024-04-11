FROM mcr.microsoft.com/cbl-mariner/base/ruby:3

RUN tdnf install -y tar && gem install fpm

WORKDIR /work

ENTRYPOINT [ "fpm" ] 
