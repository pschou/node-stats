// License:
//     MIT License, Copyright github@paulschou.com  2020 November
//
//     Many thanks / credit goes to phuslu@hotmail.com for the source of many functions

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

)


var Dockers = []Docker{}
var dms = make(map[string]string, 0)
var blk_dev = make(map[string]string, 0)
var docker_labels = make(map[string]string, 0)
var msec int64

var split func(string, int) []string = regexp.MustCompile(`\s+`).Split

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

	name    string
	body    bytes.Buffer
	preread map[string]string
}

func (m *Metrics) Files() []string {
	files := []string{}
	for f := range m.preread {
		files = append(files, f)
	}
	return files
}

func (m *Metrics) ReadFile(filename string) (string, error) {
	s, err := ioutil.ReadFile(filename)
	return string(s), err
}

var metric_count = make(map[string]int, 0)

func (m *Metrics) PrintType(name string, typ string, help string) {
	m.name = name
	if help != "" {
		m.body.WriteString(fmt.Sprintf("# HELP %s %s.\n", name, help))
	}
	if metric_count[name] == 0 {
		m.body.WriteString(fmt.Sprintf("# TYPE %s %s\n", name, typ))
	}
	metric_count[name]++
}

func (m *Metrics) PrintFloat(labels string, value float64) {
	if labels != "" {
		m.body.WriteString(fmt.Sprintf("%s{%s} ", m.name, labels))
	} else {
		m.body.WriteString(fmt.Sprintf("%s ", m.name))
	}

	m.body.WriteString(fmt.Sprintf("%-16g %d\n", value, msec))
}

func (m *Metrics) PrintStr(labels string, value string) {
	if labels != "" {
		m.body.WriteString(fmt.Sprintf("%s{%s} ", m.name, labels))
	} else {
		m.body.WriteString(fmt.Sprintf("%s ", m.name))
	}

	m.body.WriteString(fmt.Sprintf("%s %d\n", value, msec))
}

func (m *Metrics) PrintInt(labels string, value int64) {
	if labels != "" {
		m.body.WriteString(fmt.Sprintf("%s{%s} ", m.name, labels))
	} else {
		m.body.WriteString(fmt.Sprintf("%s ", m.name))
	}

	m.body.WriteString(fmt.Sprintf("%d %d\n", value, msec))
}

func (m *Metrics) PrintRaw(s string) {
	m.body.WriteString(s)
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

func (m *Metrics) CollectMemory() error {
	s, err := m.ReadFile("/proc/meminfo")
	s = strings.Replace(strings.Replace(s, "(", "_", -1), ")", "", -1)

	_, kv := (ProcFile{Text: s, Sep: ":"}).KV()

	for key, value := range kv {
		parts := split(value, -1)
		if len(parts) == 0 {
			continue
		}

		size, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		if len(parts) == 2 {
			size *= 1024
		}

		m.PrintType(fmt.Sprintf("node_memory_%s", key), "gauge", "")
		m.PrintInt("", size)
	}
	err = filepath.Walk("/sys/fs/cgroup/memory", func(path string, info os.FileInfo, err error) error {
		if info.Name() == "memory.stat" {
			t := filepath.Dir(path[22:])
			if t != "." {
				lbl := ""
				if strings.HasPrefix(t, "docker/") {
					docker_id := t[7:]
					lbl = docker_labels[docker_id]
				}
				if strings.HasPrefix(t, "system.slice/") && strings.HasSuffix(t, ".service") {
					service_id := t[13 : len(t)-8]
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
		if info.Name() == "cpuacct.usage_percpu" {
			t := filepath.Dir(path[27:])
			if t != "." {
				lbl := ""
				if strings.HasPrefix(t, "docker/") {
					docker_id := t[7:]
					lbl = docker_labels[docker_id]
				}
				if strings.HasPrefix(t, "system.slice/") && strings.HasSuffix(t, ".service") {
					service_id := t[13 : len(t)-8]
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
		m.PrintType("node_procs_map_count_maximum", "gauge", "Maximum threads")
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


	err = filepath.Walk("/sys/fs/cgroup/blkio", func(path string, info os.FileInfo, err error) error {
		if info.Name() == "blkio.throttle.io_serviced" {
			t := filepath.Dir(path[21:])
			if t != "." {
				lbl := ""
				if strings.HasPrefix(t, "docker/") {
					docker_id := t[7:]
					lbl = docker_labels[docker_id]
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
						if parts[len(parts)-1] == "0" {
							continue
						}
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
	Size       int64
	Used       int64
	Avail      int64
	Files      int64
	FilesFree  int64
}

/*
func (m *Metrics) CollectFilesystem() error {
	s, err := m.ReadFile("/proc/mounts")
	if err != nil {
		return err
	}

	mountpoints := make(map[string]FilesystemInfo)

	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		parts := split(strings.TrimSpace(scanner.Text()), -1)
		device, mountpoint, fstype := parts[0], parts[1], parts[2]

		if regexp.MustCompile(defIgnoredMountPoints).MatchString(mountpoint) {
			continue
		}
		if regexp.MustCompile(defIgnoredFSTypes).MatchString(fstype) {
			continue
		}

		mountpoints[mountpoint] = FilesystemInfo{
			MountPoint: mountpoint,
			FSType:     fstype,
			Device:     device,
		}
	}

	cmd := "df"
	//if m.Client.hasTimeout {
	//	cmd = "timeout 3 df"
	//}
	args := ""
	for mountpoint := range mountpoints {
		args += " " + mountpoint
	}
	cmd = fmt.Sprintf("%s %s ; %s -i %s", cmd, args, cmd, args)

	//s, err = m.Client.Execute(cmd)
	//if err != nil && s == "" {
	//	return err
	//}

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

	m.PrintType("node_filesystem_size", "gauge", "Filesystem size in bytes")
	for _, fi := range mountpoints {
		m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Size)
	}

	m.PrintType("node_filesystem_free", "gauge", "Filesystem free space in bytes")
	for _, fi := range mountpoints {
		m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Size-fi.Used)
	}

	m.PrintType("node_filesystem_avail", "gauge", "Filesystem space available to non-root users in bytes")
	for _, fi := range mountpoints {
		m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Avail)
	}

	m.PrintType("node_filesystem_files", "gauge", "Filesystem inodes number")
	for _, fi := range mountpoints {
		m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.Files)
	}

	m.PrintType("node_filesystem_files_free", "gauge", "Filesystem inodes free number")
	for _, fi := range mountpoints {
		m.PrintInt(fmt.Sprintf("device=\"%s\",fstype=\"%s\",mountpoint=\"%s\"", fi.Device, fi.FSType, fi.MountPoint), fi.FilesFree)
	}

	return nil
}
*/

func (m *Metrics) CollectAll() (string, error) {

	Dockers = getDocker() // This needs to be called early because it is used for later routines
	msec = time.Now().UnixNano() / 1e6

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
		t := fmt.Sprintf(",docker_name=\"%s\",docker_image=\"%s\"", d.cont.Name, d.Image)
		m.CollectNetdev(d.cont.State.Pid, t)
		docker_labels[d.Id] = t
	}
	m.CollectNFTables()

	m.CollectDiskstats()
	//m.CollectMDStat()
	m.CollectStat()
	m.CollectMemory()
	//m.CollectFilesystem()
	//m.CollectTextfile()
	//m.CollectScript()

	//ens192, _ := netlink.LinkByName("vethcc9bdbc")
	//Qdisc, _ := netlink.QdiscList(ens192)
	//fmt.Println("qd: ", Qdisc)

	return m.body.String(), nil
}

func SetProcessName(name string) error {
	if runtime.GOOS == "linux" {
		argv0str := (*reflect.StringHeader)(unsafe.Pointer(&os.Args[0]))
		argv0 := (*[1 << 30]byte)(unsafe.Pointer(argv0str.Data))[:len(name)+1]

		n := copy(argv0, name+string(0))
		if n < len(argv0) {
			argv0[n] = 0
		}
	}

	return nil
}

func main() {
	//http.HandleFunc("/metrics", func(rw http.ResponseWriter, req *http.Request) {
	m := Metrics{}

	s, err := m.CollectAll()
	fmt.Println(s)
	if err != nil {
		//	http.Error(rw, err.Error(), http.StatusServiceUnavailable)
		return
	}

}
