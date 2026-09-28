# Build stage
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/fiveagent ./app/fiveagent

# Run stage: static binary plus bubblewrap, so the sandbox (enabled by
# default) always has a backend inside the container. distroless has no
# package manager, so the final image is debian slim with bwrap
# installed; the binary itself stays fully static.
# Bubblewrap needs unprivileged user namespaces (on by default in Docker
# and most kernels; on Ubuntu 24.04+ hosts see fiveagent.yml.example).
# Alternative: set sandbox.backend=docker and mount /var/run/docker.sock.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends bubblewrap ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /bin/fiveagent /bin/fiveagent
EXPOSE 8080
ENTRYPOINT ["/bin/fiveagent"]
