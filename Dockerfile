FROM golang:1.26.6-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY push ./push
COPY scheduler ./scheduler
COPY api ./api
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /push-dispatch ./cmd/push-dispatch

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /push-dispatch /push-dispatch
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/push-dispatch"]
CMD ["serve"]
