# 公号智写 v2. Build from the v2/ directory: docker build -t gonghao-v2 .
FROM node:20-alpine AS web
ARG NPM_REGISTRY=https://registry.npmjs.org/
WORKDIR /web
ENV npm_config_registry=${NPM_REGISTRY}
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS go
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/embed.go ./web/embed.go
COPY --from=web /web/dist ./web/dist
ARG COMMIT=dev
ARG BUILT_AT=
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X gonghao/internal/version.Commit=${COMMIT} -X gonghao/internal/version.BuiltAt=${BUILT_AT}" \
    -o /out/app ./cmd/app && \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/migrate-v1 ./cmd/migrate-v1

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=go /out/app /app/app
COPY --from=go /out/migrate-v1 /app/migrate-v1
ENV V2_ADDR=:8090
EXPOSE 8090
ENTRYPOINT ["/app/app"]
