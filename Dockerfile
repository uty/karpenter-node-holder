# syntax=docker/dockerfile:1
# FROM golang:1.21.1 as builder
#
# WORKDIR /app
#
# ARG TARGETARCH
#
# COPY main.go go.mod go.sum .
# # ENV GO111MODULE=on
# RUN go mod download
# # RUN CGO_ENABLED=0 GOOS=linux go build -o karpenter-node-holder main.go
# RUN GOARCH=${TARGETARCH} GOOS=linux go build -o karpenter-node-holder-linux-${TARGETARCH} main.go
#
# FROM golang:1.21.1-alpine
#
# WORKDIR /app
#
# ARG TARGETARCH
#
# COPY --from=builder /app/karpenter-node-holder-linux-${TARGETARCH} ./karpenter-node-holder
#
# ENV SLEEP_DURATION=15
# ENV HOLD_ANNOTAION="karpenter.sh/do-not-evict"
#
# # Define the command to run the application
# CMD ["./karpenter-node-holder"]

FROM golang:1.21.1-alpine

WORKDIR /app
COPY main.go go.mod go.sum .
# ENV GO111MODULE=on
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o karpenter-node-holder main.go

ENV HOLD_DURATION=15
ENV HOLD_ANNOTAION="karpenter.sh/do-not-consolidate"

# Define the command to run the application
CMD ["./karpenter-node-holder"]

