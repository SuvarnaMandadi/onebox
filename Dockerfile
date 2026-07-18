# Multi-stage build: onebox is pure Go (no cgo — see ROADMAP.md's Month 3
# decision note), so the final image needs nothing but the static binary.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /onebox ./cmd/onebox

FROM gcr.io/distroless/static-debian12
COPY --from=build /onebox /onebox
ENV ONEBOX_ADDR=:8090
ENV ONEBOX_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8090
ENTRYPOINT ["/onebox"]
