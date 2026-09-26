FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /sipline .

FROM scratch
COPY --from=build /sipline /sipline
EXPOSE 7700
ENV SIPLINE_ADDR=0.0.0.0:7700
ENTRYPOINT ["/sipline"]
