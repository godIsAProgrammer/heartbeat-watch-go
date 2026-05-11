FROM golang:1.23

ENV CGO_ENABLED=0 \
    PORT=8801

WORKDIR /app

COPY repo/ .

RUN go build -o /app/server .

EXPOSE 8801

CMD ["/app/server"]
