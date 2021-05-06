PROG_NAME := "node-stats"
VERSION = 0.1.$(shell date -u +%Y%m%d.%H%M)
FLAGS := "-s -w -X main.version=${VERSION}"


build:
	CGO_ENABLED=0 go build -ldflags=${FLAGS} -o ${PROG_NAME} ${PROG_NAME}.go docker.go nftables.go
	#CGO_ENABLED=0 gotip build -ldflags=${FLAGS} -o ${PROG_NAME} ${PROG_NAME}.go docker.go nftables.go
	#CGO_ENABLED=0 OOS=linux gotip build -ldflags=${FLAGS} -o ${PROG_NAME} ${PROG_NAME}.go docker.go nftables.go
	upx --lzma ${PROG_NAME}
	#upx -f --brute ${PROG_NAME}
	GOOS=solaris GOARCH=amd64 CGO_ENABLED=0 go build -ldflags=${FLAGS} -o ${PROG_NAME}_solaris64 ${PROG_NAME}.go docker.go nftables.go
	upx --lzma ${PROG_NAME}_solaris64

