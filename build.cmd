set GOOS=windows
set GOARCH=amd64

go build -ldflags "-s -w" -o bin/campusserver.exe ./cmd/server
go build -ldflags "-s -w" --buildmode=c-shared -o bin/libcampusserver.dll ./cmd/server