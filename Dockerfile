# --- build -------------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum VERSION ./
RUN go mod download
COPY *.go CHANGELOG.md ./
COPY internal ./internal
COPY templates ./templates
COPY assets ./assets
COPY fonts ./fonts
ARG BUILD=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.build=${BUILD}" -o /circles .

# --- runtime -----------------------------------------------------------------
# distroless static: no shell, ca-certs + a nonroot user, tiny.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /circles /circles
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --retries=3 CMD ["/circles", "-healthcheck"]
ENTRYPOINT ["/circles"]
