FROM golang:1.21.1

WORKDIR /app
COPY main.go go.mod go.sum .
# ENV GO111MODULE=on
RUN go mod download
RUN go build -o karpenter-node-holder main.go

ENV SLEEP_DURATION=15
ENV HOLD_LABEL="karpenter.sh/do-not-evict"

# Define the command to run the application
CMD ["./karpernter-node-holder]

