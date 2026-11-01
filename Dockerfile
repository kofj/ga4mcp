FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ga4mcp .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ga4mcp /usr/local/bin/ga4mcp
ENV HOME=/home/nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/ga4mcp"]
CMD ["-addr", ":8080"]
