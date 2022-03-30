FROM alpine:3.15.3
COPY dist /
VOLUME /data
WORKDIR /
EXPOSE 9000

ENTRYPOINT ["/portainer"]
