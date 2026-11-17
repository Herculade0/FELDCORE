# Build image for feldcored. The Go stage is 1.26.11 so the daemon
# is compiled with the HTTP/1 and HTTP/2 fixes from that release.
FROM node:22-bookworm AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26.11-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w" -o /out/feldcored ./cmd/feldcored

FROM debian:bookworm-slim
COPY --from=build /out/feldcored /usr/local/bin/feldcored
ENTRYPOINT ["feldcored"]
