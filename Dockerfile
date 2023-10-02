FROM golang:1.21.1-alpine

WORKDIR /app
COPY main.go go.mod go.sum .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o karpenter-node-holder main.go

ENV HOLD_DURATION=15
ENV HOLD_ANNOTAION="karpenter.sh/do-not-consolidate"

# Define the command to run the application
CMD ["./karpenter-node-holder"]

