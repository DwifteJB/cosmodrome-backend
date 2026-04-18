# alpine
FROM golang:1.26.2-alpine

# apk get cgo dependencies
RUN apk add --no-cache build-base

# set working directory
WORKDIR /app

# copy go mod and sum files
COPY go.mod go.sum ./

# download dependencies
RUN go mod download

# copy source code
COPY . .

# build the application
RUN go build -o server .

# expose port
# 3000 usually
EXPOSE 3000

# run the application
CMD ["./server"]

