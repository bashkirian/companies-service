FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/companies ./cmd/companies

# Dev-only token issuer for hosts without Go: docker build --target devtoken.
FROM build AS devtoken-build
RUN CGO_ENABLED=0 go build -trimpath -o /out/devtoken ./tools/devtoken

FROM gcr.io/distroless/static-debian12:nonroot AS devtoken
COPY --from=devtoken-build /out/devtoken /devtoken
ENTRYPOINT ["/devtoken"]

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/companies /companies
COPY configs/config.yaml /etc/companies/config.yaml
ENV CONFIG_PATH=/etc/companies/config.yaml
EXPOSE 8080 9090
USER nonroot:nonroot
ENTRYPOINT ["/companies"]
CMD ["serve"]
