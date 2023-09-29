
FROM golang:1.16

WORKDIR /app
COPY main.go .
RUN go get -u k8s.io/client-go/...
RUN go build -o karpenter-node-holder main.go

ENV SLEEP_DURATION=15
ENV HOLD_LABEL="karpenter.sh/do-not-evict"

# Define the command to run the application
CMD ["./karpernter-node-holder]

