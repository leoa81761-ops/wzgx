# 公号智写 v2. Build from the v2/ directory: docker build -t gonghao-v2 .
FROM node:20-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG COMMIT=dev
ARG BUILT_AT=
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X gonghao/internal/version.Commit=${COMMIT} -X gonghao/internal/version.BuiltAt=${BUILT_AT}" \
    -o /out/app ./cmd/app

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=go /out/app /app/app
COPY --from=web /web/dist /app/web/dist
ENV V2_ADDR=:8090 V2_WEB_DIR=/app/web/dist
EXPOSE 8090
ENTRYPOINT ["/app/app"]
