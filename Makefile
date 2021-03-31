PROG_NAME := "node-stats"
FLAGS := "-s -w"


build:
	CGO_ENABLED=0 go build -ldflags=${FLAGS} -o ${PROG_NAME} ${PROG_NAME}.go docker.go nftables.go
	#CGO_ENABLED=0 gotip build -ldflags=${FLAGS} -o ${PROG_NAME} ${PROG_NAME}.go docker.go nftables.go
	#CGO_ENABLED=0 OOS=linux gotip build -ldflags=${FLAGS} -o ${PROG_NAME} ${PROG_NAME}.go docker.go nftables.go
	upx --lzma ${PROG_NAME}
	#upx -f --brute ${PROG_NAME}
