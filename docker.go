package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
)

func Docker_sock(req string) string {
	c, err := net.Dial("unix", "/var/run/docker.sock")
	if err != nil {
		return ""
	}
	defer c.Close()

	reply := make(chan string)
	go func(r io.Reader) {
		str := ""
		buf := make([]byte, 10240)
		for {
			n, err := r.Read(buf[:])
			if err != nil {
				return
			}
			str = str + string(buf[0:n])
			if n < 10240 {
				reply <- str
				return
			}
		}
	}(c)

	_, err = c.Write([]byte(fmt.Sprintf("GET %s HTTP/1.0\nHost: localhost\nAccept: */*\n\n", req)))
	if err != nil {
		return ""
	}
	str := <-reply
	return strings.SplitN(str, "\r\n\r\n", 2)[1]
}
func getDocker() []Docker {
	dockers := make([]Docker, 0)
	err := json.Unmarshal([]byte(Docker_sock("/v1.18/containers/json")), &dockers)
	if err != nil {
		return dockers
	}
	for i, d := range dockers {
		cont := Container{}
		err := json.Unmarshal([]byte(Docker_sock(fmt.Sprintf("/v1.18/containers/%s/json", d.Id))), &cont)
		if err != nil {
			continue
		}
		dockers[i].cont = cont
		//fmt.Println("stats: ", Docker_sock(fmt.Sprintf("/v1.19/containers/%s/stats?stream=0", d.Id)))
	}
	return dockers
}

type Docker struct {
	Id      string
	Names   []string
	Image   string
	Created int64
	State   string
	Labels  map[string]string
	cont    Container
}
type ContainerState struct {
	Status     string
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Pid        int64
}
type Container struct {
	State        ContainerState
	LogPath      string
	Name         string
	RestartCount int64
}
