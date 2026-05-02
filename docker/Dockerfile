# syntax=docker/dockerfile:1
# Single Dockerfile parameterized by SERVICE (bot|web|worker) and PORT.
# Build via docker-compose with build.args.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${SERVICE}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget tzdata
WORKDIR /app
COPY --from=build /out/app /app/app
ARG PORT=8080
ENV HEALTHCHECK_PORT=${PORT}
EXPOSE ${PORT}
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
  CMD wget --quiet --spider http://localhost:${HEALTHCHECK_PORT}/health || exit 1
ENTRYPOINT ["/app/app"]
