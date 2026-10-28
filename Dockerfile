FROM golang:1.27-alpine AS build

RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /ical-splitter .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /ical-splitter /ical-splitter
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ical-splitter"]
