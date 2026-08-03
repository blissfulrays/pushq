# Build the dashboard
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY web/ ./
RUN npm run build

# Build the server (pure Go — no CGO)
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/build ./web/build
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/blissfulrays/pushq/internal/version.Version=${VERSION} -X github.com/blissfulrays/pushq/internal/version.Commit=${COMMIT}" \
    -o /pushq ./cmd/pushq

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /pushq /pushq
VOLUME ["/data"]
EXPOSE 8080
ENV PUSHQ_DB=sqlite:/data/pushq.db
ENTRYPOINT ["/pushq"]
CMD ["serve"]
