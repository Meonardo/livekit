set GOOS=windows
set GOARCH=amd64

go build -ldflags "-s -w" -o bin/livekit.exe ./cmd/server