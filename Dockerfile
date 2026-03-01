FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/makestack-core ./cmd/makestack-core

# ----

FROM alpine:3.19

COPY --from=build /out/makestack-core /usr/local/bin/makestack-core

EXPOSE 8420

ENTRYPOINT ["makestack-core"]
