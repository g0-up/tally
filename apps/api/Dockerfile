# syntax=docker/dockerfile:1
# The patch release matches the go line of go.mod: GOTOOLCHAIN=local refuses an older one.
FROM golang:1.26.9-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/tally ./cmd/tally

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/tally /tally
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/tally"]
CMD ["serve"]
