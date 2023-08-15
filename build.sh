export GOOS=darwin
export GOARCH=arm64

go build -ldflags "-s -w" -o bin/campusserver ./cmd/server