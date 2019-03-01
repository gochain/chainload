# build stage
FROM golang:1.12-alpine AS build-env
RUN apk --no-cache add build-base git bzr mercurial gcc
ENV D=/chainload
WORKDIR $D
# cache dependencies
ADD go.mod $D
ADD go.sum $D
RUN go mod download
# build
ADD . $D
RUN cd $D && go install

# final stage
FROM alpine
RUN apk add --no-cache ca-certificates curl
COPY --from=build-env /go/bin/chainload /usr/local/bin/chainload
ENTRYPOINT ["chainload"]
