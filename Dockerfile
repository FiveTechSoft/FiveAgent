# Build stage
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/fiveagent ./cmd/fiveagent

# Run stage: one static binary, no runtime needed
FROM gcr.io/distroless/static-debian12
COPY --from=build /bin/fiveagent /bin/fiveagent
EXPOSE 8000
ENTRYPOINT ["/bin/fiveagent"]
