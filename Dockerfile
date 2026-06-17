FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /granola-backup ./cmd/granola-backup

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /granola-backup /granola-backup
VOLUME /token /output
ENTRYPOINT ["/granola-backup"]
CMD ["-refresh-token-file", "/token/refresh_token.json", "-output", "/output"]
