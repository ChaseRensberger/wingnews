FROM node:20-alpine AS css
WORKDIR /app

COPY package.json bun.lock input.css index.html ./
COPY templates ./templates

RUN npm install
RUN npm run css:build

FROM golang:1.25-alpine AS builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=css /app/output.css ./output.css

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o wingnews .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=builder /app/wingnews /app/wingnews
COPY --from=builder /app/index.html /app/index.html
COPY --from=builder /app/templates /app/templates
COPY --from=builder /app/public /app/public
COPY --from=builder /app/output.css /app/output.css

EXPOSE 3000
CMD ["/app/wingnews"]
