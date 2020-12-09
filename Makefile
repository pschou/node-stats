PROG_NAME := "node-stats"


build:
	CGO_ENABLED=0 go build -o ${PROG_NAME} ${PROG_NAME}.go docker.go nftables.go

tiny:
	tinygo build -o ${PROG_NAME} .
