package containermcp

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// NOTE: Every limit only restricts the container; zero keeps the engine's default and a negative value would lift a limit.
type Limits struct {
	Memory            int64
	MemoryReservation int64
	MemorySwap        int64
	NanoCPUs          int64
	CPUShares         int64
	CPUPeriod         int64
	CPUQuota          int64
	CPUSet            string
	PidsLimit         int64
	// NOTE: Nil keeps the engine's default; 0 is a real setting that stops the container's memory from being swapped out.
	MemorySwappiness *int64
}

var (
	capName    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	hostLabel  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	cpuSetList = regexp.MustCompile(`^[0-9]+([-,][0-9]+)*$`)
	signalName = regexp.MustCompile(`^(SIG)?[A-Z0-9+-]+$`)
	noSpace    = regexp.MustCompile(`^\S+$`)
)

// NOTE: suid and dev are left out because they would widen what the container's own tmpfs can do.
var tmpfsFlags = map[string]bool{"rw": true, "ro": true, "exec": true, "noexec": true, "nosuid": true, "nodev": true}

var tmpfsValues = map[string]bool{"size": true, "mode": true, "uid": true, "gid": true, "nr_inodes": true}

func (s Service) validateOptions() error {
	for _, c := range s.CapDrop {
		if !capName.MatchString(c) {
			return fmt.Errorf("cap_drop entry %q is not a capability name", c)
		}
	}
	if err := s.Limits.validate(); err != nil {
		return err
	}
	for _, f := range []struct{ name, value string }{{"hostname", s.Hostname}, {"domainname", s.Domainname}} {
		if f.value != "" && !hostLabel.MatchString(f.value) {
			return fmt.Errorf("%s %q is not a valid name", f.name, f.value)
		}
	}
	for _, ip := range s.DNS {
		if _, err := netip.ParseAddr(ip); err != nil {
			return fmt.Errorf("dns entry %q is not an IP address", ip)
		}
	}
	for _, d := range s.DNSSearch {
		if !hostLabel.MatchString(d) {
			return fmt.Errorf("dns_search entry %q is not a valid domain", d)
		}
	}
	for _, o := range s.DNSOptions {
		if !noSpace.MatchString(o) {
			return fmt.Errorf("dns_opt entry %q must not contain whitespace", o)
		}
	}
	if s.StopSignal != "" && !signalName.MatchString(s.StopSignal) {
		return fmt.Errorf("stop signal %q is not a signal name or number", s.StopSignal)
	}
	return nil
}

func (l Limits) validate() error {
	for _, f := range []struct {
		name  string
		value int64
	}{
		{"memory limit", l.Memory},
		{"memory reservation", l.MemoryReservation},
		{"memory and swap limit", l.MemorySwap},
		{"cpus", l.NanoCPUs},
		{"cpu shares", l.CPUShares},
		{"cpu period", l.CPUPeriod},
		{"cpu quota", l.CPUQuota},
		{"pids limit", l.PidsLimit},
	} {
		if f.value < 0 {
			return fmt.Errorf("%s must not be negative, since a negative value lifts the limit", f.name)
		}
	}
	if l.CPUSet != "" && !cpuSetList.MatchString(l.CPUSet) {
		return fmt.Errorf("cpuset %q must look like 0-2 or 0,1", l.CPUSet)
	}
	if s := l.MemorySwappiness; s != nil && (*s < 0 || *s > 100) {
		return fmt.Errorf("memory swappiness must be between 0 and 100, got %d", *s)
	}
	return nil
}

// NOTE: An empty string is valid and keeps the engine's defaults, which are rw, noexec, nosuid and nodev.
func validateTmpfsOptions(opts string) error {
	if opts == "" {
		return nil
	}
	for opt := range strings.SplitSeq(opts, ",") {
		key, value, hasValue := strings.Cut(opt, "=")
		switch {
		case tmpfsFlags[key] && !hasValue:
		case tmpfsValues[key] && hasValue && noSpace.MatchString(value):
		case key == "suid" || key == "dev":
			return fmt.Errorf("option %q is not allowed because it widens what the tmpfs can do", key)
		default:
			return fmt.Errorf("option %q is not supported", opt)
		}
	}
	return nil
}
