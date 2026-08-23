FROM golang:1.22-bookworm

ENV GOPROXY=https://goproxy.cn,direct
ENV GOTOOLCHAIN=local
ENV CGO_ENABLED=0

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build ./... && go build -o /app/seiswatch .

EXPOSE 8080

CMD ["/app/seiswatch", "-addr", ":8080"]
