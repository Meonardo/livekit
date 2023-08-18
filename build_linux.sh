export GOOS=linux
export GOARCH=amd64

go build -ldflags "-s -w" -o bin/campusserver ./cmd/server