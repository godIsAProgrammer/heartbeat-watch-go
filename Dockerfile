FROM golang:1.23

WORKDIR /app

COPY . .

RUN go test ./... \
    && git init -b main \
    && git config user.email "docker@example.test" \
    && git config user.name "Docker Build" \
    && git add . \
    && git commit -m "Initial heartbeat watch fixture"

ENV PORT=8801
EXPOSE 8801

CMD ["go", "run", "."]
