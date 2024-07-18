package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	apitypes "github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
)

func Docker_sock(req string) string {
	c, err := net.DialTimeout("unix", "/var/run/docker.sock", time.Second*3)
	if err != nil {
		return ""
	}
	defer c.Close()

	reply := make(chan string)
	go func(r io.Reader) {
		// Write and then read
		_, err = c.Write([]byte(fmt.Sprintf("GET %s HTTP/1.0\nHost: localhost\nAccept: */*\n\n", req)))
		if err != nil {
			reply <- ""
			return
		}

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

	select {
	case str := <-reply:
		return strings.SplitN(str, "\r\n\r\n", 2)[1]
	case <-time.After(3 * time.Second):
		break
	}
	return ""
}

var Dockers = make(map[string]apitypes.ContainerJSON)

func getDocker() []apitypes.Container {
	ctx := context.Background()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, containertypes.ListOptions{})
	if err != nil {
		panic(err)
	}

	for _, c := range containers {
		cj, err := cli.ContainerInspect(ctx, c.ID)
		//fmt.Printf("Container:%#v\n", c)
		//fmt.Printf("Container details:%#v\n", cj.ContainerJSONBase)
		if err == nil {
			Dockers[c.ID] = cj
		}
	}

	/*
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
		}*/
	return containers
}

/*type Docker struct {
	Id      string
	Names   []string
	Image   string
	Created int64
	State   string
	Labels  map[string]string
	cont    Container
}

/*type ContainerState struct {
	Status     string
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Pid        int64
	StartedAt  time.Time
	FinishedAt time.Time
}
type Container struct {
	State        ContainerState
	LogPath      string
	Name         string
	RestartCount int64
}

type Container struct {
	ID      string        `json:"Id"`
	Created time.Time     `json:"Created"`
	Path    string        `json:"Path"`
	Args    []interface{} `json:"Args"`
	State   struct {
		Status     string    `json:"Status"`
		Running    bool      `json:"Running"`
		Paused     bool      `json:"Paused"`
		Restarting bool      `json:"Restarting"`
		OOMKilled  bool      `json:"OOMKilled"`
		Dead       bool      `json:"Dead"`
		Pid        int64     `json:"Pid"`
		ExitCode   int       `json:"ExitCode"`
		Error      string    `json:"Error"`
		StartedAt  time.Time `json:"StartedAt"`
		FinishedAt time.Time `json:"FinishedAt"`
	} `json:"State"`
	Image        string `json:"Image"`
	LogPath      string `json:"LogPath"`
	Name         string `json:"Name"`
	RestartCount int64  `json:"RestartCount"`
	HostConfig   struct {
		NetworkMode   string `json:"NetworkMode"`
		RestartPolicy struct {
			Name              string `json:"Name"`
			MaximumRetryCount int    `json:"MaximumRetryCount"`
		} `json:"RestartPolicy"`
		AutoRemove           bool          `json:"AutoRemove"`
		VolumeDriver         string        `json:"VolumeDriver"`
		VolumesFrom          interface{}   `json:"VolumesFrom"`
		CapAdd               interface{}   `json:"CapAdd"`
		CapDrop              interface{}   `json:"CapDrop"`
		CgroupnsMode         string        `json:"CgroupnsMode"`
		DNS                  []interface{} `json:"Dns"`
		DNSOptions           []interface{} `json:"DnsOptions"`
		DNSSearch            []interface{} `json:"DnsSearch"`
		ExtraHosts           interface{}   `json:"ExtraHosts"`
		GroupAdd             interface{}   `json:"GroupAdd"`
		IpcMode              string        `json:"IpcMode"`
		Cgroup               string        `json:"Cgroup"`
		Links                interface{}   `json:"Links"`
		OomScoreAdj          int           `json:"OomScoreAdj"`
		PidMode              string        `json:"PidMode"`
		Privileged           bool          `json:"Privileged"`
		PublishAllPorts      bool          `json:"PublishAllPorts"`
		ReadonlyRootfs       bool          `json:"ReadonlyRootfs"`
		SecurityOpt          interface{}   `json:"SecurityOpt"`
		UTSMode              string        `json:"UTSMode"`
		UsernsMode           string        `json:"UsernsMode"`
		ShmSize              int           `json:"ShmSize"`
		Runtime              string        `json:"Runtime"`
		Isolation            string        `json:"Isolation"`
		CPUShares            int           `json:"CpuShares"`
		Memory               int           `json:"Memory"`
		NanoCpus             int           `json:"NanoCpus"`
		CgroupParent         string        `json:"CgroupParent"`
		BlkioWeight          int           `json:"BlkioWeight"`
		BlkioWeightDevice    []interface{} `json:"BlkioWeightDevice"`
		BlkioDeviceReadBps   interface{}   `json:"BlkioDeviceReadBps"`
		BlkioDeviceWriteBps  interface{}   `json:"BlkioDeviceWriteBps"`
		BlkioDeviceReadIOps  interface{}   `json:"BlkioDeviceReadIOps"`
		BlkioDeviceWriteIOps interface{}   `json:"BlkioDeviceWriteIOps"`
		CPUPeriod            int           `json:"CpuPeriod"`
		CPUQuota             int           `json:"CpuQuota"`
		CPURealtimePeriod    int           `json:"CpuRealtimePeriod"`
		CPURealtimeRuntime   int           `json:"CpuRealtimeRuntime"`
		CpusetCpus           string        `json:"CpusetCpus"`
		CpusetMems           string        `json:"CpusetMems"`
		Devices              []interface{} `json:"Devices"`
		DeviceCgroupRules    interface{}   `json:"DeviceCgroupRules"`
		DeviceRequests       interface{}   `json:"DeviceRequests"`
		KernelMemory         int           `json:"KernelMemory"`
		KernelMemoryTCP      int           `json:"KernelMemoryTCP"`
		MemoryReservation    int           `json:"MemoryReservation"`
		MemorySwap           int           `json:"MemorySwap"`
		MemorySwappiness     interface{}   `json:"MemorySwappiness"`
		OomKillDisable       bool          `json:"OomKillDisable"`
		PidsLimit            interface{}   `json:"PidsLimit"`
		Ulimits              interface{}   `json:"Ulimits"`
		CPUCount             int           `json:"CpuCount"`
		CPUPercent           int           `json:"CpuPercent"`
		IOMaximumIOps        int           `json:"IOMaximumIOps"`
		IOMaximumBandwidth   int           `json:"IOMaximumBandwidth"`
	} `json:"HostConfig"`
	GraphDriver struct {
		Data struct {
			LowerDir  string `json:"LowerDir"`
			MergedDir string `json:"MergedDir"`
			UpperDir  string `json:"UpperDir"`
			WorkDir   string `json:"WorkDir"`
		} `json:"Data"`
		Name string `json:"Name"`
	} `json:"GraphDriver"`
	Volumes struct {
		EtcPki string `json:"/etc/pki"`
	} `json:"Volumes"`
	VolumesRW struct {
		EtcPki bool `json:"/etc/pki"`
	} `json:"VolumesRW"`
	Config struct {
		Hostname     string      `json:"Hostname"`
		Domainname   string      `json:"Domainname"`
		User         string      `json:"User"`
		AttachStdin  bool        `json:"AttachStdin"`
		AttachStdout bool        `json:"AttachStdout"`
		AttachStderr bool        `json:"AttachStderr"`
		Tty          bool        `json:"Tty"`
		OpenStdin    bool        `json:"OpenStdin"`
		StdinOnce    bool        `json:"StdinOnce"`
		Env          []string    `json:"Env"`
		Cmd          interface{} `json:"Cmd"`
		Image        string      `json:"Image"`
		Volumes      interface{} `json:"Volumes"`
		WorkingDir   string      `json:"WorkingDir"`
		Entrypoint   []string    `json:"Entrypoint"`
		OnBuild      interface{} `json:"OnBuild"`
		Labels       struct {
			Description string `json:"description"`
			Owner       string `json:"owner"`
		} `json:"Labels"`
		MacAddress      string `json:"MacAddress"`
		NetworkDisabled bool   `json:"NetworkDisabled"`
		ExposedPorts    struct {
			Eight127TCP struct {
			} `json:"8127/tcp"`
		} `json:"ExposedPorts"`
		VolumeDriver string `json:"VolumeDriver"`
		Memory       int    `json:"Memory"`
		MemorySwap   int    `json:"MemorySwap"`
		CPUShares    int    `json:"CpuShares"`
		Cpuset       string `json:"Cpuset"`
	} `json:"Config"`
	NetworkSettings struct {
		SandboxKey             string      `json:"SandboxKey"`
		SecondaryIPAddresses   interface{} `json:"SecondaryIPAddresses"`
		SecondaryIPv6Addresses interface{} `json:"SecondaryIPv6Addresses"`
		Gateway                string      `json:"Gateway"`
		GlobalIPv6Address      string      `json:"GlobalIPv6Address"`
		GlobalIPv6PrefixLen    int         `json:"GlobalIPv6PrefixLen"`
		IPAddress              string      `json:"IPAddress"`
		MacAddress             string      `json:"MacAddress"`
	} `json:"NetworkSettings"`
}*/
