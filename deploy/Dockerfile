FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /hub-router ./cmd/hub-router

FROM scratch
COPY --from=builder /hub-router /hub-router
EXPOSE 8080
ENTRYPOINT ["/hub-router"]
