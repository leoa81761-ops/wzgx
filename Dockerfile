FROM node:22-alpine AS web
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/dist ./internal/web/dist
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /out/gonghao ./cmd/gonghao

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gonghao /gonghao
ENV ADDR=:8000 \
    GOMAXPROCS=4 \
    GOMEMLIMIT=3GiB
EXPOSE 8000
USER nonroot:nonroot
HEALTHCHECK --interval=15s --timeout=5s --start-period=15s --retries=8 \
  CMD ["/gonghao", "healthcheck"]
ENTRYPOINT ["/gonghao"]
CMD ["serve"]
