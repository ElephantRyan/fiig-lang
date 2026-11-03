# The fiig runtime image: the `fiig` binary, static, on a distroless base.
# An application builds on it with its own source:
#
#   FROM fiig:v0.1.0-alpha.1
#   COPY . /app
#   CMD ["serve", "-caps", "/app/caps.json", "/app", "myHttpAdapter"]
#
# and runs with its configuration in the environment and its database on a
# volume:
#
#   docker run -p 8080:8080 -v app-data:/data -e DB_PATH=/data/app.db my-app
#
# Build from the repository root:
#
#   docker build --build-arg VERSION=v0.1.0-alpha.1 -t fiig:v0.1.0-alpha.1 .

# The build stage runs on the builder's own platform and cross-compiles, so
# a multi-platform build (linux/amd64, linux/arm64) needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine AS build
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY . .
# No cgo: the SQLite driver is pure Go, so the binary is static.
ENV CGO_ENABLED=0
RUN cd cmd/fiig && GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags "-s -w -X fiig-lang/compiler/version.Version=${VERSION}" -o /out/fiig .

# distroless/static carries CA certificates (for Socket.connectTls) and runs
# as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/fiig /usr/local/bin/fiig
WORKDIR /app
VOLUME /data
ENV LISTEN_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/fiig"]
CMD ["version"]
