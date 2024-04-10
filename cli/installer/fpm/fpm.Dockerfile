FROM mcr.microsoft.com/cbl-mariner/base/ruby:3

RUN gem install fpm

WORKDIR /work

ENTRYPOINT [ "fpm" ] 
