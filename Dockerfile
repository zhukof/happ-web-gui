# Build upstream HAPP from its official repository
FROM golang:1.26-bookworm AS happ-build
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/*
ARG HAPP_REF=main
RUN git clone https://github.com/zhukof/happ-cli.git /happ-src && cd /happ-src && git checkout "$HAPP_REF"
WORKDIR /happ-src
RUN go build -trimpath -o /out/happ ./cmd/happ

FROM node:22-bookworm AS ui-build
WORKDIR /ui
COPY frontend/package*.json ./
RUN npm install
COPY frontend/ .
RUN npm run build

FROM golang:1.26-bookworm AS api-build
WORKDIR /src
COPY backend/go.mod ./
COPY backend/ ./
COPY --from=ui-build /backend/web/dist/ ./web/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/happ-control .

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=happ-build /out/happ /usr/local/bin/happ
COPY --from=api-build /out/happ-control /usr/local/bin/happ-control
RUN mkdir -p /data
VOLUME ["/data"]
EXPOSE 8080 11080 11081
ENTRYPOINT ["/usr/local/bin/happ-control"]
