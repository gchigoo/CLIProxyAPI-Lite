FROM golang:1.26-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=lite-dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" -o /cli-proxy-api ./cmd/server

FROM alpine:3.24
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /CLIProxyAPI
COPY --from=builder /cli-proxy-api ./CLIProxyAPI
COPY config.example.yaml ./config.example.yaml
EXPOSE 8317
ENV TZ=Asia/Shanghai
CMD ["./CLIProxyAPI"]
