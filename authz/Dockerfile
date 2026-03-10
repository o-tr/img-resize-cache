FROM golang:1.21-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/authz .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/authz /authz
USER nonroot:nonroot
EXPOSE 9000
ENTRYPOINT ["/authz"]

