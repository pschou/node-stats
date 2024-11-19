// License:
//     MIT License, Copyright phuslu@hotmail.com
// Modified by pschou 2020

package main

import (
	"bufio"
	"bytes"

	//"compress/gzip"
	//"encoding/base64"
	"fmt"
	//"io"
	"io/ioutil"
	//"net"
	"path/filepath"
	//"net/http"
	"log"
	"os"
	"os/exec"

	//"path"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/araddon/dateparse"
	"github.com/pschou/go-params"

	"golang.org/x/crypto/ssh"
	//"gopkg.in/alecthomas/kingpin.v2"
	//"gopkg.in/yaml.v2"
	//"github.com/prometheus/common/log"
	//"github.com/prometheus/common/version"
	//"github.com/vishvananda/netlink"
)

//var (
//	configFile = kingpin.Flag("config.file", "Remote node exporter configuration file.").Default("remote_node_exporter.yml").String()
//)

/*var (
	Port       = os.Getenv("PORT")
	SshHost    = os.Getenv("SSH_HOST")
	SshPort    = os.Getenv("SSH_PORT")
	SshUser    = os.Getenv("SSH_USER")
	SshPass    = os.Getenv("SSH_PASS")
	SshKey     = os.Getenv("SSH_KEY")
	SshScript  = os.Getenv("SSH_SCRIPT")
	RemoteAddr = os.Getenv("REMOTE_ADDR")
)*/

/*var TextfilePath string = func() string {
	s := os.Getenv("TEXTFILE_PATH")
	if s == "" {
		s = "/var/lib/prometheus/node-exporter"
	}
	return strings.TrimSuffix(s, "/") + "/"
}()*/

// var Dockers = []types.Container{}
var dms = make(map[string]string, 0)
var blk_dev = make(map[string]string, 0)
var docker_labels = make(map[string]string, 0)
var msec string

var service_list = make(map[string]struct{}, 0)

var PreReadFileList []string = []string{
	"/etc/storage/system_time",
	"/proc/diskstats",
	"/proc/driver/rtc",
	"/proc/loadavg",
	"/proc/meminfo",
	"/proc/mounts",
	"/proc/net/arp",
	"/proc/net/dev",
	"/proc/net/netstat",
	"/proc/net/snmp",
	"/proc/net/sockstat",
	"/proc/stat",
	"/proc/sys/fs/file-nr",
	"/proc/sys/kernel/random/entropy_avail",
	"/proc/sys/net/netfilter/nf_conntrack_count",
	"/proc/sys/net/netfilter/nf_conntrack_max",
	"/proc/vmstat",
	"/tmp/proc/mdstat",
	//TextfilePath + "*.prom",
}

var split func(string, int) []string = regexp.MustCompile(`\s+`).Split

type Client struct {
	Addr   string
	Config *ssh.ClientConfig

	client     *ssh.Client
	timeOffset time.Duration
	hasTimeout bool
	script     string
	mu         sync.Mutex
}

func (c *Client) connect() error {
	c.client = nil

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return nil
	}

	var err error
	c.client, err = ssh.Dial("tcp", c.Addr, c.Config)

	if err != nil {
		log.Printf("ssh.Dial(\"tcp\", %#v, ...) error: %+v\n", c.Addr, err)
		return err
	} else {
		log.Printf("ssh.Dial(\"tcp\", %#v, ...) ok\n", c.Addr)
	}

	session, err := c.client.NewSession()
	if err != nil {
		log.Printf("%v.NewSession() error: %+v, reconnecting...\n", c.client, err)
		return err
	}

	var b bytes.Buffer
	session.Stdout = &b

	session.Run("date +%z; test -f /usr/bin/timeout; echo $?")
	parts := strings.Split(b.String(), "\n")
	log.Printf("session.Run() return %#v\n", parts)
	s := strings.TrimSpace(parts[0])
	if len(s) == 5 {
		h, _ := strconv.Atoi(s[1:3])
		m, _ := strconv.Atoi(s[3:5])
		c.timeOffset = time.Duration((h*60+m)*60) * time.Second

		if s[0] == '-' {
			c.timeOffset = -c.timeOffset
		}

	}
	if parts[1] == "0" {
		c.hasTimeout = true
	}
	log.Printf("%#v timezone is %+v, has timeout command is %+v\n", c.Addr, c.timeOffset, c.hasTimeout)

	return err

}

func (c *Client) TimeOffset() time.Duration {
	return c.timeOffset
}

func (c *Client) Execute(cmd string) (string, error) {
	log.Printf("%T.Execute(%#v)\n", c, cmd)

	//if c.client == nil {
	//	c.connect()
	//}

	retry := 2
	for i := 0; i < retry; i += 1 {
		session, err := c.client.NewSession()
		if err != nil {
			if i < retry-1 {
				log.Printf("NewSession() error: %+v, reconnecting...\n", err)
				c.client.Close()
				c.connect()
				continue
			}
			return "", err
		}
		defer session.Close()

		var b bytes.Buffer
		session.Stdout = &b

		err = session.Run(cmd)

		return b.String(), err
	}

	return "", nil
}

type ProcFile struct {
	Text     string
	Sep      string
	SkipRows int
}

func (pf ProcFile) sep() string {
	if pf.Sep != "" {
		return pf.Sep
	}
	return " "
}

func (pf ProcFile) Int() (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(pf.Text), 10, 64)
}

func (pf ProcFile) Float() (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(pf.Text), 64)
}

func (pf ProcFile) Strings() []string {
	sep := pf.Sep
	if sep == "" {
		sep = `\s+`
	}
	return regexp.MustCompile(sep).Split(strings.TrimSpace(pf.Text), -1)
}

func (pf ProcFile) KV() ([]string, map[string]string) {
	h := make([]string, 0)
	m := make(map[string]string)

	scanner := bufio.NewScanner(strings.NewReader(pf.Text))

	for i := 0; i < pf.SkipRows; i += 1 {
		if !scanner.Scan() {
			return h, m
		}
		h = append(h, scanner.Text())
	}

	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), pf.sep(), 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		m[key] = value
	}

	return h, m
}

func (pf ProcFile) KVS() ([]string, map[string][]string) {
	h := make([]string, 0)
	m := make(map[string][]string)

	scanner := bufio.NewScanner(strings.NewReader(pf.Text))

	for i := 0; i < pf.SkipRows; i += 1 {
		if !scanner.Scan() {
			return h, m
		}
		h = append(h, scanner.Text())
	}

	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), pf.sep(), 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if v, ok := m[key]; ok {
			m[key] = append(v, value)
		} else {
			m[key] = []string{value}
		}
	}

	return h, m
}

type Metrics struct {
	Client *Client

	name string
	body bytes.Buffer
	//preread map[string]string
	metric_count map[string]int
}

/*
func (m *Metrics) PreRead() error {
	m.preread = make(map[string]string)

	cmd := "/bin/fgrep \"\" " + strings.Join(PreReadFileList, " ")

	output, _ := m.Client.Execute(cmd)

	split := func(s string) map[string]string {
		m := make(map[string]string)
		var lastname string
		var b bytes.Buffer

		scanner := bufio.NewScanner(strings.NewReader(s))
		for scanner.Scan() {
			parts := strings.SplitN(scanner.Text(), ":", 2)
			if len(parts) != 2 {
				continue
			}

			filename := strings.TrimSpace(parts[0])
			line := parts[1]

			if filename != lastname {
				if lastname != "" {
					m[lastname] = b.String()
				}
				b.Reset()
				lastname = filename
			}

			b.WriteString(line)
			b.WriteString("\n")
		}
		m[lastname] = b.String()
		return m
	}

	m.preread = split(output)

	for _, filename := range PreReadFileList {
		if _, ok := m.preread[filename]; !ok {
			m.preread[filename] = ""
		}
	}

	return nil
}*/

/*func (m *Metrics) Files() []string {
	files := []string{}
	for f := range m.preread {
		files = append(files, f)
	}
	return files
}*/

func (m *Metrics) ReadFile(filename string) (string, error) {
	//s, ok := m.preread[filename]
	//if ok {
	//	return s, nil
	//}

	//return m.Client.Execute("/bin/cat " + filename)
	s, err := ioutil.ReadFile(filename)
	return string(s), err
}

func (m *Metrics) PrintType(name string, typ string, help string) {
	if m.metric_count == nil {
		m.metric_count = make(map[string]int, 0)
	}
	m.name = name
	if m.metric_count[name] == 0 {
		if help != "" {
			m.body.WriteString(fmt.Sprintf("# HELP %s %s.\n", name, help))
		}
		m.body.WriteString(fmt.Sprintf("# TYPE %s %s\n", name, typ))
	}
	m.metric_count[name]++
}

func (m *Metrics) PrintFloat(labels string, value float64) {
	if labels != "" {
		m.body.WriteString(fmt.Sprintf("%s{%s} ", m.name, labels))
	} else {
		m.body.WriteString(fmt.Sprintf("%s ", m.name))
	}

	m.body.WriteString(fmt.Sprintf("%-16g%s\n", value, msec))
	//if value >= 1000000 {
	//	m.body.WriteString(fmt.Sprintf("%e\n", value))
	//} else {
	//	m.body.WriteString(fmt.Sprintf("%f\n", value))
	//}
}

func (m *Metrics) PrintBool(labels string, value bool) {
	if labels != "" {
		m.body.WriteString(fmt.Sprintf("%s{%s} ", m.name, labels))
	} else {
		m.body.WriteString(fmt.Sprintf("%s ", m.name))
	}

	if value {
		m.body.WriteString(fmt.Sprintf("1%s\n", msec))
	} else {
		m.body.WriteString(fmt.Sprintf("0%s\n", msec))
	}
}

func (m *Metrics) PrintStr(labels string, value string) {
	if labels != "" {
		m.body.WriteString(fmt.Sprintf("%s{%s} ", m.name, labels))
	} else {
		m.body.WriteString(fmt.Sprintf("%s ", m.name))
	}

	m.body.WriteString(fmt.Sprintf("%s%s\n", value, msec))
}

func (m *Metrics) PrintInt(labels string, value int64) {
	if labels != "" {
		m.body.WriteString(fmt.Sprintf("%s{%s} ", m.name, labels))
	} else {
		m.body.WriteString(fmt.Sprintf("%s ", m.name))
	}

	//if value >= 1000000 {
	//	m.body.WriteString(fmt.Sprintf("%g\n", float64(value)))
	//} else {
	m.body.WriteString(fmt.Sprintf("%d%s\n", value, msec))
	//}
}

func (m *Metrics) PrintRaw(s string) {
	m.body.WriteString(s)
}

func (m *Metrics) CollectTime() error {
	var t time.Time
	var nsec int64

	s, err := m.ReadFile("/proc/driver/rtc")

	if s != "" {
		_, kv := (ProcFile{Text: s, Sep: ":"}).KV()
		date := kv["rtc_date"] + " " + kv["rtc_time"]
		t, err = time.Parse("2006-01-02 15:04:05", date)
		nsec = t.Unix()
		//nsec += int64(m.Client.TimeOffset() / time.Second)
	}

	if nsec == 0 {
		s, err = m.ReadFile("/etc/storage/system_time")
		nsec, err = (ProcFile{Text: s}).Int()
	}

	if nsec == 0 {
		s, err = m.Client.Execute("date +%s")
		nsec, err = (ProcFile{Text: s}).Int()
	}

	if nsec != 0 {
		m.PrintType("node_time", "counter", "System time in seconds since epoch (1970)")
		m.PrintInt("", nsec)
		//msec = nsec * 1e3
	}

	return err
}

func (m *Metrics) CollectLoadavg() error {
	s, err := m.ReadFile("/proc/loadavg")
	if err != nil {
		return err
	}

	parts := (ProcFile{Text: s}).Strings()
	if len(parts) < 3 {
		return fmt.Errorf("Unknown loadavg %#v", s)
	}

	m.PrintType("node_load1", "gauge", "1m load average")
	m.PrintStr("", parts[0])
	m.PrintType("node_load5", "gauge", "5m load average")
	m.PrintStr("", parts[1])
	m.PrintType("node_load15", "gauge", "5m load average")
	m.PrintStr("", parts[2])

	m.PrintType("node_procs_threads", "gauge", "Thread count")
	m.PrintStr("", strings.SplitN(parts[3], "/", 2)[1])

	return nil
}

func (m *Metrics) CollectFilefd() error {
	s, err := m.ReadFile("/proc/sys/fs/file-nr")
	parts := (ProcFile{Text: s}).Strings()

	if len(parts) < 3 {
		return fmt.Errorf("Unknown file-nr %#v", s)
	}

	if allocated, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
		m.PrintType("node_filefd_allocated", "gauge", "File descriptor statistics: allocated")
		m.PrintInt("", allocated)
	}

	if maximum, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
		m.PrintType("node_filefd_maximum", "gauge", "File descriptor statistics: maximum")
		m.PrintInt("", maximum)
	}

	return err
}

func (m *Metrics) CollectNfConntrack() error {
	var s string
	var n int64
	var err error

	s, err = m.ReadFile("/proc/sys/net/netfilter/nf_conntrack_count")
	if s != "" {
		if n, err = (ProcFile{Text: s}).Int(); err == nil {
			m.PrintType("node_nf_conntrack_entries", "gauge", "Number of currently allocated flow entries for connection tracking")
			m.PrintInt("", n)
		}
	}

	s, err = m.ReadFile("/proc/sys/net/netfilter/nf_conntrack_max")
	if s != "" {
		if n, err = (ProcFile{Text: s}).Int(); err == nil {
			m.PrintType("node_nf_conntrack_entries_limit", "gauge", "Maximum size of connection tracking table")
			m.PrintInt("", n)
		}
	}

	return err
}

func (m *Metrics) CollectKernel() error {
	out, err := exec.Command("/usr/bin/uname", "-r").Output()
	m.PrintType("node_kernel_info", "gauge", "Running kernel")
	m.PrintInt(fmt.Sprintf("version=%q", strings.TrimSpace(string(out))), 1)
	sr, err := os.Open("/etc/system-release")
	if err == nil {
		defer sr.Close()
		dat := make([]byte, 256)
		ct, _ := sr.Read(dat)
		parts := strings.SplitN(strings.TrimSpace(string(dat[:ct])), " release ", 2)
		if len(parts) == 2 {
			m.PrintType("node_system_release_info", "gauge", "System Release Info")
			m.PrintInt(fmt.Sprintf("name=%q,version=%q", parts[0], parts[1]), 1)
		}
	}
	return err
}
func (m *Metrics) CollectSystemd() error {
	for proc := range service_list {
		out, err := exec.Command("/usr/bin/systemctl", "--no-pager", "show", proc).Output()
		if err == nil {
			prop := make(map[string]string, 0)
			for _, line := range strings.Split(string(out), "\n") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					prop[parts[0]] = strings.TrimSpace(string(parts[1]))
				}
				//fmt.Println("NS output = ", parts)
			}
			m.PrintType("node_systemd_unit_state", "gauge", "Systemd unit's current state")
			for _, state := range []string{"activating", "active", "deactiviating", "failed", "inactive"} {
				m.PrintBool(fmt.Sprintf("name=%q,state=%q", proc, state), state == prop["ActiveState"])
			}

			m.PrintType("node_systemd_unit_start_time_seconds", "gauge", "Systemd start time since boot")
			//val, _ := time.Parse("2006-01-02T15:04:05.000Z", prop["ActiveEnterTimestamp"])
			//m.PrintInt(fmt.Sprintf("name=%q", proc), val.Unix())
			val, _ := strconv.ParseUint(prop["ExecMainStartTimestampMonotonic"], 10, 64)
			m.PrintStr(fmt.Sprintf("name=%q", proc), fmt.Sprintf("%d.%06d", val/1e6, val%1e6))
		}
	}
	//fmt.Println("NS err = ", err)
	return nil
}

func (m *Metrics) CollectMemory() error {
	s, err := m.ReadFile("/proc/meminfo")
	s = strings.Replace(strings.Replace(s, "(", "_", -1), ")", "", -1)

	_, kv := (ProcFile{Text: s, Sep: ":"}).KV()

	for key, value := range kv {
		//fmt.Printf("memory - key  %v value %v\n", key, value)
		parts := split(value, -1)
		if len(parts) == 0 {
			continue
		}

		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		unit := ""
		if len(parts) == 2 && parts[1] == "kB" {
			size *= 1024
			unit = "_bytes"
		}

		m.PrintType(fmt.Sprintf("node_memory_%s"+unit, key), "gauge", "")
		m.PrintInt("", size)
	}
	err = filepath.Walk("/sys/fs/cgroup/memory", func(path string, info os.FileInfo, err error) error {
		if info != nil && info.Name() == "memory.stat" {
			t := filepath.Dir(path[22:])
			if t != "." {
				lbl := ""
				if strings.HasPrefix(t, "docker/") {
					docker_id := strings.TrimSuffix(t[7:], ".scope")
					if docker_labels[docker_id] != "" {
						lbl = "," + docker_labels[docker_id]
					}
				} else if strings.HasPrefix(t, "system.slice/docker-") {
					docker_id := strings.TrimSuffix(t[20:], ".scope")
					if docker_labels[docker_id] != "" {
						lbl = "," + docker_labels[docker_id]
					}
				}
				if strings.HasPrefix(t, "system.slice/") && strings.HasSuffix(t, ".service") {
					service_id := t[13 : len(t)-8]
					service_list[service_id] = struct{}{}
					lbl = fmt.Sprintf("%s,service=\"%s\"", lbl, service_id)
				}

				s, _ := m.ReadFile(path)
				lines := strings.Split(s, "\n")
				for _, line := range lines {
					parts := split(strings.TrimSpace(line), -1)
					if !strings.HasPrefix(parts[0], "total_") || parts[1] == "0" {
						continue
					}
					m.PrintType(fmt.Sprintf("node_cgroup_memory_%s", parts[0]), "gauge", "")
					m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), parts[1])
				}

				s, _ = m.ReadFile(filepath.Dir(path) + "/memory.usage_in_bytes")
				if strings.TrimSpace(s) != "0" {
					m.PrintType(fmt.Sprintf("node_cgroup_memory_bytes"), "gauge", "")
					m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), strings.TrimSpace(s))

					s, err := m.ReadFile(filepath.Dir(path) + "/memory.memsw.usage_in_bytes")
					if err == nil {
						m.PrintType(fmt.Sprintf("node_cgroup_memory_swap_bytes"), "gauge", "")
						m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), strings.TrimSpace(s))
					}

					s, err = m.ReadFile(filepath.Dir(path) + "/memory.swappiness")
					if err == nil {
						m.PrintType(fmt.Sprintf("node_cgroup_memory_swappiness"), "gauge", "")
						m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), strings.TrimSpace(s))
					}

					s, err = m.ReadFile(filepath.Dir(path) + "/memory.limit_in_bytes")
					if err == nil {
						m.PrintType(fmt.Sprintf("node_cgroup_memory_limit_bytes"), "gauge", "")
						m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), strings.TrimSpace(s))
					}

					s, err = m.ReadFile(filepath.Dir(path) + "/memory.memsw.limit_in_bytes")
					if err == nil {
						m.PrintType(fmt.Sprintf("node_cgroup_memory_swap_limit_bytes"), "gauge", "")
						m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), strings.TrimSpace(s))
					}

					s, err = m.ReadFile(filepath.Dir(path) + "/memsw.max_usage_in_bytes")
					if err == nil {
						m.PrintType(fmt.Sprintf("node_cgroup_memory_max_usage_bytes"), "gauge", "")
						m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), strings.TrimSpace(s))
					}
				}
			}
		}
		return nil
	})

	return err
}

func (m *Metrics) CollectNetstat() error {
	var s1, s2 string
	var err error

	s1, err = m.ReadFile("/proc/net/netstat")
	s2, err = m.ReadFile("/proc/net/snmp")
	_, kv := (ProcFile{Text: (s1 + s2), Sep: ":"}).KVS()

	for key, values := range kv {
		if len(values) != 2 {
			continue
		}

		v1 := split(values[0], -1)
		v2 := split(values[1], -1)

		for i, v := range v1 {
			n, err := strconv.ParseInt(v2[i], 10, 64)
			if err != nil {
				continue
			}
			m.PrintType(fmt.Sprintf("node_netstat_%s_%s", key, v), "gauge", "")
			m.PrintInt("", n)
		}
	}

	return err
}

func (m *Metrics) CollectSockstat() error {
	s, err := m.ReadFile("/proc/net/sockstat")
	_, kv := (ProcFile{Text: s, Sep: ":"}).KV()

	for key, value := range kv {
		vs := split(value, -1)
		for i := 0; i < len(vs)-1; i += 2 {
			k := vs[i]
			n, err := strconv.ParseInt(vs[i+1], 10, 64)
			if err != nil {
				continue
			}
			m.PrintType(fmt.Sprintf("node_sockstat_%s_%s", key, k), "gauge", "")
			m.PrintInt("", n)
		}
	}

	return err
}

func (m *Metrics) CollectVmstat() error {
	s, err := m.ReadFile("/proc/vmstat")
	_, kv := (ProcFile{Text: s}).KV()

	for key, value := range kv {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		m.PrintType(fmt.Sprintf("node_vmstat_%s", key), "gauge", "")
		m.PrintInt("", n)
	}

	return err
}

var CPUModes []string = []string{
	"user",
	"nice",
	"system",
	"idle",
	"iowait",
	"irq",
	"softirq",
	"steal",
	"guest",
	"guest_nice",
}

func (m *Metrics) CollectStat() error {
	s, err := m.ReadFile("/proc/stat")
	_, kv := (ProcFile{Text: s}).KV()

	if v, ok := kv["btime"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			m.PrintType("node_boot_time", "gauge", "Node boot time, in unixtime")
			m.PrintInt("", n)
		}
	}

	if v, ok := kv["ctxt"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			m.PrintType("node_context_switches", "counter", "Total number of context switches")
			m.PrintInt("", n)
		}
	}

	if v, ok := kv["processes"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			m.PrintType("node_forks", "counter", "Total number of forks")
			m.PrintInt("", n)
		}
	}

	if v, ok := kv["intr"]; ok {
		vs := split(v, -1)
		if n, err := strconv.ParseInt(vs[0], 10, 64); err == nil {
			m.PrintType("node_intr", "counter", "Total number of interrupts serviced")
			m.PrintInt("", n)
		}
	}

	if v, ok := kv["procs_blocked"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			m.PrintType("node_procs_blocked", "gauge", "Number of processes blocked waiting for I/O to complete")
			m.PrintInt("", n)
		}
	}

	if v, ok := kv["procs_running"]; ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			m.PrintType("node_procs_running", "gauge", "Number of processes in runnable state")
			m.PrintInt("", n)
		}
	}

	m.PrintType("node_cpu_seconds", "counter", "Seconds the cpus spent in each mode")
	cores := int64(0)
	for key, value := range kv {
		if key == "cpu" || !strings.HasPrefix(key, "cpu") {
			continue
		}
		cores++

		vs := split(value, -1)
		for i, mode := range CPUModes {
			if i == len(vs) {
				break
			}
			if n, err := strconv.ParseInt(vs[i], 10, 64); err == nil {
				m.PrintStr(fmt.Sprintf("cpu=\"%s\",mode=\"%s\"", key, mode), fmt.Sprintf("%d.%02d", n/100, n%100))
			}
		}
	}
	m.PrintType("node_cpu_count", "gauge", "Core count")
	m.PrintInt("", cores)

	err = filepath.Walk("/sys/fs/cgroup/cpu,cpuacct", func(path string, info os.FileInfo, err error) error {
		if info != nil && info.Name() == "cpuacct.usage_percpu" {
			t := filepath.Dir(path[27:])
			if t != "." {
				lbl := ""
				if strings.HasPrefix(t, "docker/") {
					docker_id := strings.TrimSuffix(t[7:], ".scope")
					if docker_labels[docker_id] != "" {
						lbl = "," + docker_labels[docker_id]
					}
				} else if strings.HasPrefix(t, "system.slice/docker-") {
					docker_id := strings.TrimSuffix(t[20:], ".scope")
					if docker_labels[docker_id] != "" {
						lbl = "," + docker_labels[docker_id]
					}
				}
				if strings.HasPrefix(t, "system.slice/") && strings.HasSuffix(t, ".service") {
					service_id := t[13 : len(t)-8]
					service_list[service_id] = struct{}{}
					lbl = fmt.Sprintf("%s,service=\"%s\"", lbl, service_id)
				}

				s, _ = m.ReadFile(path)
				lines := strings.Split(s, "\n")
				for _, line := range lines {
					parts := split(strings.TrimSpace(line), -1)
					if parts[0] == "0" || line == "" {
						continue
					}
					total := int64(0)
					for icore, usage := range parts {
						n, _ := strconv.ParseInt(usage, 10, 64)
						total = total + n
						m.PrintType(fmt.Sprintf("node_cgroup_cpu_core_seconds"), "gauge", "")
						m.PrintStr(fmt.Sprintf("cgroup=\"%s\",core=\"%d\"%s", t, icore, lbl), fmt.Sprintf("%d.%09d", n/1e9, n%1e9))
					}
					m.PrintType(fmt.Sprintf("node_cgroup_cpu_seconds"), "gauge", "")
					m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), fmt.Sprintf("%d.%09d", total/1e9, total%1e9))
				}

				s, err := m.ReadFile("/sys/fs/cgroup/cpu,cpuacct/" + t + "/cpu.shares")
				if err == nil {
					m.PrintType(fmt.Sprintf("node_cgroup_cpu_shares"), "gauge", "")
					m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), strings.TrimSpace(s))
				}
			}
		}
		return nil
	})

	return err
}

func (m *Metrics) CollectNetdev(pid int64, subLabel string) error {
	if subLabel != "" {
		subLabel = "," + subLabel
	}
	s := ""
	var err error
	if pid == 0 {
		s, err = m.ReadFile("/proc/net/dev")
	} else {
		s, err = m.ReadFile(fmt.Sprintf("/proc/%d/net/dev", pid))
	}
	hs, kv := (ProcFile{Text: s, Sep: ":", SkipRows: 2}).KV()

	virt_devs := []string{}
	virt_dir, err := os.Open("/sys/devices/virtual/net/")
	if err == nil {
		defer virt_dir.Close()
		virt_devs, err = virt_dir.Readdirnames(0)
	}

	if len(hs) != 2 {
		return nil
	}

	faces := strings.Split(hs[1], "|")
	rfaces := split(strings.TrimSpace(faces[1]), -1)
	tfaces := split(strings.TrimSpace(faces[2]), -1)

	metrics := make(map[string][]string)
	for key, value := range kv {
		metrics[key] = split(value, -1)
	}

	for i := 0; i < len(rfaces)+len(tfaces); i += 1 {
		var inter, face string
		if i < len(rfaces) {
			inter = "receive"
			face = rfaces[i]
		} else {
			inter = "transmit"
			face = tfaces[i-len(rfaces)]
		}

		m.PrintType(fmt.Sprintf("node_network_%s_%s", inter, face), "gauge", "")

	interface_loop:
		for key, values := range metrics {

			for _, virt := range virt_devs {
				if key == virt {
					continue interface_loop
				}
			}
			n, err := strconv.ParseInt(values[i], 10, 64)
			if err != nil {
				continue
			}
			m.PrintInt(fmt.Sprintf("device=\"%s\"%s", key, subLabel), n)
		}
	}

	return err
}

func (m *Metrics) CollectArp() error {
	s, err := m.ReadFile("/proc/net/arp")
	if err != nil {
		return err
	}

	_, kv := (ProcFile{Text: s, SkipRows: 1}).KV()

	devices := make(map[string]int64)
	for _, value := range kv {
		vs := split(value, -1)
		dev := vs[len(vs)-1]
		if n, ok := devices[dev]; !ok {
			devices[dev] = 1
		} else {
			devices[dev] = n + 1
		}
	}

	m.PrintType("node_arp_entries", "gauge", "ARP entries by device")
	for key, value := range devices {
		m.PrintInt(fmt.Sprintf("device=\"%s\"", key), value)
	}

	return err
}

func (m *Metrics) CollectEntropy() error {
	s, err := m.ReadFile("/proc/sys/kernel/random/entropy_avail")
	if err != nil {
		return err
	}

	n, err := (ProcFile{Text: s}).Int()
	if err != nil {
		return err
	}

	m.PrintType("node_entropy_available_bits", "gauge", "Bits of available entropy")
	m.PrintInt("", n)

	return err
}

func (m *Metrics) CollectThreads() error {
	s, err := m.ReadFile("/proc/sys/kernel/threads-max")
	if err == nil {
		m.PrintType("node_procs_threads_maximum", "gauge", "Maximum threads")
		m.PrintStr("", strings.TrimSpace(s))
	}

	s, err = m.ReadFile("/proc/sys/vm/max_map_count")
	if err == nil {
		m.PrintType("node_procs_map_count_maximum", "gauge", "Maximum number of memory map areas a process may have")
		m.PrintStr("", strings.TrimSpace(s))
	}

	s, err = m.ReadFile("/proc/sys/kernel/pid_max")
	if err == nil {
		m.PrintType("node_procs_pid_maximum", "gauge", "Maximum threads")
		m.PrintStr("", strings.TrimSpace(s))
	}

	return err
}

var DiskStatsMode []string = []string{
	"reads_completed",
	"reads_merged",
	"sectors_read",
	"read_time_ms",
	"writes_completed",
	"writes_merged",
	"sectors_written",
	"write_time_ms",
	"io_now",
	"io_time_ms",
	"io_time_weighted",
}

func (m *Metrics) CollectDiskstats() error {
	s, err := m.ReadFile("/proc/diskstats")
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(strings.NewReader(s))

	devices := make(map[string][]string)
	for scanner.Scan() {
		parts := split(strings.TrimSpace(scanner.Text()), -1)
		if len(parts) < 2 {
			continue
		}

		dev := parts[2]
		//fmt.Println("dev", dev, dev[0:2])
		if len(dev) > 3 && dev[0:3] == "dm-" {
			if dms[dev] == "" {
				dm_s, err := m.ReadFile(fmt.Sprintf("/sys/block/%s/dm/name", dev))
				if err == nil {
					t := strings.TrimSpace(dm_s)
					dms[dev] = t
					dev = t
				}
			} else {
				dev = dms[dev]
			}
		}
		values := parts[3:14]
		blk_dev[parts[0]+":"+parts[1]] = dev

		devices[dev] = values
	}

	for i, mode := range DiskStatsMode {
		m.PrintType(fmt.Sprintf("node_disk_%s", mode), "gauge", "")
		for dev, values := range devices {
			n, err := strconv.ParseInt(values[i], 10, 64)
			if err != nil {
				continue
			}
			m.PrintInt(fmt.Sprintf("device=\"%s\"", dev), n)
		}
	}

	//fmt.Println("readdir", findDirs("/sys/fs/cgroup/blkio", "blkio.throttle.io_serviced"))

	err = filepath.Walk("/sys/fs/cgroup/blkio", func(path string, info os.FileInfo, err error) error {
		if info != nil && info.Name() == "blkio.throttle.io_serviced" {
			t := filepath.Dir(path[21:])
			if t != "." {
				lbl := ""
				if strings.HasPrefix(t, "docker/") {
					docker_id := strings.TrimSuffix(t[7:], ".scope")
					if docker_labels[docker_id] != "" {
						lbl = "," + docker_labels[docker_id]
					}
				} else if strings.HasPrefix(t, "system.slice/docker-") {
					docker_id := strings.TrimSuffix(t[20:], ".scope")
					if docker_labels[docker_id] != "" {
						lbl = "," + docker_labels[docker_id]
					}
				}
				if strings.HasPrefix(t, "system.slice/") && strings.HasSuffix(t, ".service") {
					service_id := t[13 : len(t)-8]
					lbl = fmt.Sprintf("%s,service=\"%s\"", lbl, service_id)
				}

				s_files := [2]string{}
				s_files[0], _ = m.ReadFile(path)
				s_files[1], _ = m.ReadFile("/sys/fs/cgroup/blkio/" + t + "/blkio.throttle.io_service_bytes")
				for i, s := range s_files {
					ty := ""
					if i == 1 {
						ty = "_bytes"
					}
					lines := strings.Split(s, "\n")
					for _, line := range lines {
						parts := split(line, -1)
						//if parts[len(parts)-1] == "0" {
						//	continue
						//}
						if len(parts) == 3 {
							tdev := blk_dev[parts[0]]
							if tdev != "" {
								parts[0] = tdev
							}
							m.PrintType(fmt.Sprintf("node_cgroup_blkio_%s%s", parts[1], ty), "gauge", "")
							m.PrintStr(fmt.Sprintf("device=\"%s\",cgroup=\"%s\"%s", parts[0], t, lbl), parts[2])
						} else if len(parts) == 2 {
							m.PrintType(fmt.Sprintf("node_cgroup_blkio_%s%s", parts[0], ty), "gauge", "")
							m.PrintStr(fmt.Sprintf("cgroup=\"%s\"%s", t, lbl), parts[1])
						}
					}
				}
			}
		}
		return nil
	})

	return err
}

func (m *Metrics) CollectMDStat() error {
	_, err := m.ReadFile("/tmp/proc/mdstat")
	if err != nil {
		return err
	}

	// TODO FILL THIS OUT!!! //

	// TODO MAKE ONE FOR ZFS!!! ///

	// TODO MAKE ONE FOR XFS!!! ///

	return nil
}

// https://github.com/prometheus/node_exporter/blob/master/collector/filesystem_linux.go
const (
	defIgnoredMountPoints = "^/(sys|proc|dev)($|/)"
	defIgnoredFSTypes     = "^(sysfs|procfs|autofs|nfs|nfs4|cgroup|fuse\\.lxcfs)$"
)

type FilesystemInfo struct {
	MountPoint string
	FSType     string
	Device     string
	MntFlags   string
	ReadOnly   bool
	Size       int64
	Used       int64
	Avail      int64
	Files      int64
	FilesFree  int64
	FilesUsed  int64
}

func (m *Metrics) CollectFilesystem() error {
	s, err := m.ReadFile("/proc/mounts")
	if err != nil {
		return err
	}

	mountpoints := make(map[string]FilesystemInfo)

	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		parts := split(strings.TrimSpace(scanner.Text()), -1)
		device, mountpoint, fstype, flags := parts[0], parts[1], parts[2], parts[3]

		if regexp.MustCompile(defIgnoredMountPoints).MatchString(mountpoint) {
			continue
		}
		if regexp.MustCompile(defIgnoredFSTypes).MatchString(fstype) {
			continue
		}

		readOnly := false
		for _, f := range strings.Split(flags, ",") {
			//fmt.Println("flag == ", f)
			if f == "ro" {
				readOnly = true
			}
		}

		mountpoints[mountpoint] = FilesystemInfo{
			MountPoint: mountpoint,
			FSType:     fstype,
			Device:     device,
			MntFlags:   flags,
			ReadOnly:   readOnly,
		}
	}

	/*
			cmd := "df"
			if m.Client.hasTimeout {
				cmd = "timeout 3 df"
			}
			args := ""
			for mountpoint := range mountpoints {
				args += " " + mountpoint
			}
			cmd = fmt.Sprintf("%s %s ; %s -i %s", cmd, args, cmd, args)

			s, err = m.Client.Execute(cmd)
			if err != nil && s == "" {
				return err
			}

			if s == "" {
				return fmt.Errorf("df timed out")
			}

		scanner = bufio.NewScanner(strings.NewReader(s))
		isInodesLine := false
		for scanner.Scan() {
			parts := split(strings.TrimSpace(scanner.Text()), -1)

			if parts[0] == "Filesystem" {
				isInodesLine = parts[1] == "Inodes"
				continue
			}

			size, used, avail, mountpoint := parts[1], parts[2], parts[3], parts[5]

			fi, ok := mountpoints[mountpoint]
			if !ok {
				continue
			}

			if n, err := strconv.ParseInt(size, 10, 64); err == nil {
				if !isInodesLine {
					fi.Size = n * 1024
				} else {
					fi.Files = n
				}
			}
			if n, err := strconv.ParseInt(used, 10, 64); err == nil {
				if !isInodesLine {
					fi.Used = n * 1024
				}
			}
			if n, err := strconv.ParseInt(avail, 10, 64); err == nil {
				if !isInodesLine {
					fi.Avail = n * 1024
				} else {
					fi.FilesFree = n
				}
			}

			mountpoints[mountpoint] = fi
		}

	*/
	// "--all", "--sync",
	out, err := exec.Command("df", "--block-size=1024", "--output=source,target,fstype,itotal,iavail,iused,size,avail,used").Output()
	if err == nil {

		scanner = bufio.NewScanner(strings.NewReader(string(out)))
		for scanner.Scan() {
			parts := split(strings.TrimSpace(scanner.Text()), -1)
			if parts[0] == "Filesystem" {
				continue
			}
			fi, ok := mountpoints[parts[1]]

			if ok {
				//fmt.Println("found ", parts)
				fi.FSType = parts[2]
				fi.Files, err = strconv.ParseInt(parts[3], 10, 64)
				fi.FilesFree, err = strconv.ParseInt(parts[4], 10, 64)
				fi.FilesUsed, err = strconv.ParseInt(parts[5], 10, 64)
				fi.Size, err = strconv.ParseInt(parts[6], 10, 64)
				fi.Avail, err = strconv.ParseInt(parts[7], 10, 64)
				fi.Used, err = strconv.ParseInt(parts[8], 10, 64)
				mountpoints[parts[1]] = fi
			}

		}

		m.PrintType("node_filesystem_size", "gauge", "Filesystem size in bytes")
		for _, fi := range mountpoints {
			if fi.Size > 0 {
				m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Size)
			}
		}

		m.PrintType("node_filesystem_free", "gauge", "Filesystem free space in bytes")
		for _, fi := range mountpoints {
			if fi.Size > 0 {
				m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Size-fi.Used)
			}
		}

		m.PrintType("node_filesystem_avail", "gauge", "Filesystem space available to non-root users in bytes")
		for _, fi := range mountpoints {
			if fi.Size > 0 {
				m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Avail)
			}
		}

		m.PrintType("node_filesystem_files", "gauge", "Filesystem inodes number")
		for _, fi := range mountpoints {
			if fi.Size > 0 {
				m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Files)
			}
		}

		m.PrintType("node_filesystem_files_free", "gauge", "Filesystem inodes free number")
		for _, fi := range mountpoints {
			if fi.Size > 0 {
				m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.FilesFree)
			}
		}
	}

	m.PrintType("node_filesystem_readonly", "gauge", "Filesystem readonly")
	for _, fi := range mountpoints {
		if fi.Size > 0 {
			m.PrintBool(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.ReadOnly)
		}
	}

	return nil
}

/*func (m *Metrics) CollectTextfile() error {
	for _, name := range m.Files() {
		if !strings.HasPrefix(name, TextfilePath) {
			continue
		}
		s, err := m.ReadFile(name)
		if err != nil {
			return err
		}
		m.PrintRaw(string(s))
	}
	return nil
}*/

func (m *Metrics) CollectScript() error {
	cmd := fmt.Sprintf("echo %s | base64 -d | gunzip | sh", m.Client.script)
	output, _ := m.Client.Execute(cmd)
	m.PrintRaw(output)
	return nil
}

func (m *Metrics) CollectAll() (string, error) {
	//var err error

	//err = m.PreRead()
	//if err != nil {
	//	log.Printf("%T.PreRead() error: %+v\n", m, err)
	//}

	getDocker() // This needs to be called early because it is used for later routines

	//m.CollectTime()
	m.CollectLoadavg()
	m.CollectFilefd()
	m.CollectNfConntrack()
	m.CollectNetstat()
	m.CollectSockstat()
	m.CollectVmstat()
	m.CollectArp()
	m.CollectEntropy()
	m.CollectThreads()

	m.CollectNetdev(0, "")
	for _, d := range Dockers {
		//fmt.Println("docker", d)
		t := fmt.Sprintf("docker_name=\"%s\",docker_image=\"%s\"", d.Name, d.Image)
		m.CollectNetdev(int64(d.State.Pid), t)
		docker_labels[d.ID] = t
	}

	m.PrintType("node_docker_started_at", "gauge", "Docker created time")
	for _, d := range Dockers {
		if strings.HasPrefix(d.State.StartedAt, "000") {
			continue
		}
		if t, err := dateparse.ParseAny(d.State.StartedAt); err == nil {
			m.PrintInt(docker_labels[d.ID], t.UnixNano()/1e6)
		} else {
			//m.PrintStr(docker_labels[d.ID], "NaN")
		}
	}

	m.PrintType("node_docker_finished_at", "gauge", "Docker finished time")
	for _, d := range Dockers {
		if strings.HasPrefix(d.State.FinishedAt, "000") {
			continue
		}
		if t, err := dateparse.ParseAny(d.State.FinishedAt); err == nil {
			m.PrintInt(docker_labels[d.ID], t.UnixNano()/1e6)
		} else {
			//m.PrintStr(docker_labels[d.ID], "NaN")
		}
	}

	m.PrintType("node_docker_info", "gauge", "Docker info")
	for _, d := range Dockers {
		/*
			lblarr := make(map[string]string)
			for l, v := range d.Labels {
				lblarr[strings.ToLower(l)] = v
			}
			lblstr := []string{docker_labels[d.ID]}
			reg, _ := regexp.Compile("[^a-zA-Z0-9_]+")
			for l, v := range lblarr {
				lblstr = append(lblstr, fmt.Sprintf("%s=%q", reg.ReplaceAllString(l, "_"), v))
			}
			m.PrintInt(strings.Join(lblstr, ","), 1)
		*/
		m.PrintInt(fmt.Sprintf("%s,processLabel=%q,mountLabel=%q", docker_labels[d.ID], d.ProcessLabel, d.MountLabel), 1)
	}

	m.PrintType("node_docker_running", "gauge", "Docker container is running")
	for _, d := range Dockers {
		m.PrintBool(docker_labels[d.ID], d.State.Running)
	}
	m.PrintType("node_docker_restarting", "gauge", "Docker container is running")
	for _, d := range Dockers {
		m.PrintBool(docker_labels[d.ID], d.State.Restarting)
	}
	m.PrintType("node_docker_restart_count", "gauge", "Docker restart count")
	for _, d := range Dockers {
		m.PrintInt(docker_labels[d.ID], int64(d.RestartCount))
	}
	m.PrintType("node_docker_size_rw", "gauge", "Docker container size RW")
	for _, d := range Dockers {
		if d.SizeRw != nil {
			m.PrintInt(docker_labels[d.ID], *d.SizeRw)
		}
	}
	m.PrintType("node_docker_size_root", "gauge", "Docker container size Root")
	for _, d := range Dockers {
		if d.SizeRootFs != nil {
			m.PrintInt(docker_labels[d.ID], *d.SizeRootFs)
		}
	}

	/*
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
	     StartedAt  time.Time
	     FinishedAt time.Time
	   }
	   type Container struct {
	     State        ContainerState
	     LogPath      string
	     Name         string
	     RestartCount int64
	   }
	*/

	m.CollectNFTables()

	m.CollectDiskstats()
	//m.CollectMDStat()
	m.CollectStat()
	m.CollectMemory()
	m.CollectSystemd()
	m.CollectKernel()
	m.CollectFilesystem()
	//m.CollectTextfile()
	//m.CollectScript()

	//ens192, _ := netlink.LinkByName("vethcc9bdbc")
	//Qdisc, _ := netlink.QdiscList(ens192)
	//fmt.Println("qd: ", Qdisc)

	return m.body.String(), nil
}

/*func Forward(lconn net.Conn) {
	addr := net.JoinHostPort(SshHost, SshPort)
	config := &ssh.ClientConfig{
		User: SshUser,
		Auth: []ssh.AuthMethod{
			ssh.Password(SshPass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         8 * time.Second,
	}

	c, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		log.Printf("ssh.Dial(%#v) error: %+v\n", addr, err)
		return
	}

	rconn, err := c.Dial("tcp", RemoteAddr)
	if err != nil {
		log.Printf("%T.Dial(%#v) error: %+v\n", RemoteAddr, err)
		return
	}

	go io.Copy(rconn, lconn)
	io.Copy(lconn, rconn)
}*/

func SetProcessName(name string) error {
	if runtime.GOOS == "linux" {
		argv0str := (*reflect.StringHeader)(unsafe.Pointer(&os.Args[0]))
		argv0 := (*[1 << 30]byte)(unsafe.Pointer(argv0str.Data))[:len(name)+1]

		n := copy(argv0, name+"\x00")
		if n < len(argv0) {
			argv0[n] = 0
		}
	}

	return nil
}

var version = "0"

func main() {
	params.CommandLine.Title = "node-stats, a prometheus metrics collector, Written by Paul Schou (github.com/pschou/node-stats), Version: " + version
	includeTime := params.Pres("time", "Include time in output")
	params.Parse()

	if *includeTime {
		msec = fmt.Sprintf(" %d", time.Now().UnixNano()/1e6)
	}

	fmt.Println("#ABOUT: NodeStats written by Paul Schou -- https://github.com/pschou/node-stats")
	/*
		log.AddFlags(kingpin.CommandLine)
		kingpin.Version(version.Print("remote_node_exporter"))
		kingpin.HelpFlag.Short('h')
		kingpin.Parse()

		log.Infoln("Starting remote_node_exporter", version.Info())
		log.Infoln("Build context", version.BuildContext())

		if SshHost == "" {
			type Config struct {
				Exporter []struct {
					Host   string
					Port   int
					User   string
					Pass   string
					Key    string
					Local  int
					Script string
				}
				Forward []struct {
					Host   string
					Port   int
					User   string
					Pass   string
					Key    string
					Local  int
					Remote string
				}
			}

			config := &Config{}

			exe, err := os.Executable()
			if err != nil {
				log.Fatalf("error: %v", err)
			}

			ConfigPaths := []string{
				*configFile,
				path.Join(path.Dir(exe), path.Base(*configFile)),
			}

			var data []byte
			for _, filename := range ConfigPaths {
				data, err = ioutil.ReadFile(filename)
				if err == nil {
					break
				}
			}
			if err != nil {
				log.Fatalf("error: read %+v %v", ConfigPaths, err)
			}

			err = yaml.Unmarshal(data, &config)
			if err != nil {
				log.Fatalf("error: %v", err)
			}

			for _, s := range config.Exporter {
				if s.Host == "" {
					log.Fatalf("error: %#v host is empty", s)
				}
				if s.Port == 0 {
					s.Port = 22
				}
				cmd := exec.Command(exe)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				cmd.Env = append(os.Environ(),
					"SSH_HOST="+s.Host,
					"SSH_PORT="+strconv.Itoa(s.Port),
					"SSH_USER="+s.User,
					"SSH_PASS="+s.Pass,
					"SSH_KEY="+s.Key,
					"SSH_SCRIPT="+s.Script,
					"PORT="+strconv.Itoa(s.Local),
				)
				go cmd.Run()
			}

			for _, s := range config.Forward {
				if s.Host == "" {
					log.Fatalf("error: %#v host is empty", s)
				}
				if s.Port == 0 {
					s.Port = 22
				}
				cmd := exec.Command(exe)
				cmd.Env = append(os.Environ(),
					"SSH_HOST="+s.Host,
					"SSH_PORT="+strconv.Itoa(s.Port),
					"SSH_USER="+s.User,
					"SSH_PASS="+s.Pass,
					"SSH_KEY="+s.Key,
					"PORT="+strconv.Itoa(s.Local),
					"REMOTE_ADDR="+s.Remote,
				)
				go cmd.Run()
			}

			SetProcessName("remote_node_exporter: master process " + exe)
			select {}
		}

		if SshPort == "" {
			SshPort = "22"
		}

		if RemoteAddr != "" {
			SetProcessName(fmt.Sprintf("remote_node_exporter: [%s@%s] listening %s tunneling remote %s", SshUser, SshHost, Port, RemoteAddr))
			for {
				ln, err := net.Listen("tcp", ":"+Port)
				if err != nil {
					SetProcessName(fmt.Sprintf("remote_node_exporter: [%s@%s] listening %s error: %+v", SshUser, SshHost, Port, err))
					select {}
				}

				for {
					conn, err := ln.Accept()
					if err != nil {
						time.Sleep(100 * time.Millisecond)
					}
					go Forward(conn)
				}
			}
			return
		}

		client := &Client{
			Addr: net.JoinHostPort(SshHost, SshPort),
			Config: &ssh.ClientConfig{
				User: SshUser,
				Auth: []ssh.AuthMethod{
					ssh.Password(SshPass),
				},
				HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				Timeout:         8 * time.Second,
			},
		}

		if SshKey != "" {
			key, err := ioutil.ReadFile(SshKey)
			if err != nil {
				log.Fatalf("unable to read private key: %v", err)
			}

			// Create the Signer for this private key.
			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				log.Fatalf("unable to parse private key: %v", err)
			}

			client.Config.Auth[0] = ssh.PublicKeys(signer)
		}

		if SshScript != "" {
			data, err := ioutil.ReadFile(SshScript)
			if err != nil {
				log.Fatalf("unable to read private key: %v", err)
			}

			var b bytes.Buffer
			w := gzip.NewWriter(&b)
			w.Write(data)
			w.Flush()

			client.script = base64.StdEncoding.EncodeToString(b.Bytes())
		}
	*/

	//http.HandleFunc("/metrics", func(rw http.ResponseWriter, req *http.Request) {
	m := Metrics{
		//	Client: client,
	}

	s, err := m.CollectAll()
	if err != nil {
		//	http.Error(rw, err.Error(), http.StatusServiceUnavailable)
		return
	}
	fmt.Println(s)

	/*if strings.Contains(req.Header.Get("Accept-Encoding"), "gzip") {
		rw.Header().Set("Content-Encoding", "gzip")
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusOK)
		w := gzip.NewWriter(rw)
		io.WriteString(w, s)
		w.Close()
	} else {
		io.WriteString(rw, s)
	}*/
	//})

	/*http.HandleFunc("/", func(rw http.ResponseWriter, req *http.Request) {
		io.WriteString(rw, `<html>
			<head><title>Node Exporter</title></head>
			<body>
			<h1>Node Exporter</h1>
			<p><a href="/metrics">Metrics</a></p>
			</body>
			</html>`)
	})

	SetProcessName(fmt.Sprintf("remote_node_exporter: [%s@%s] listening %s", SshUser, SshHost, Port))

	log.Fatal(http.ListenAndServe(":"+Port, nil))*/
	//fmt.Println("#EOF")
}
