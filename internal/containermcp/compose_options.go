package containermcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

// NOTE: Translates the compose fields that only restrict the container or describe its own identity.
func translateOptions(name string, svc types.ServiceConfig, out *Service) error {
	out.CapDrop = slices.Clone(svc.CapDrop)
	out.ReadOnly = svc.ReadOnly
	out.Hostname, out.Domainname, out.StopSignal = svc.Hostname, svc.DomainName, svc.StopSignal
	out.DNS = slices.Clone(svc.DNS)
	out.DNSSearch = slices.Clone(svc.DNSSearch)
	out.DNSOptions = slices.Clone(svc.DNSOpts)
	out.Limits = Limits{
		Memory:            int64(svc.MemLimit),
		MemoryReservation: int64(svc.MemReservation),
		MemorySwap:        int64(svc.MemSwapLimit),
		NanoCPUs:          nanoCPUs(svc.CPUS),
		CPUShares:         svc.CPUShares,
		CPUPeriod:         svc.CPUPeriod,
		CPUQuota:          svc.CPUQuota,
		CPUSet:            svc.CPUSet,
		PidsLimit:         svc.PidsLimit,
	}
	// NOTE: The loader accepts -1, which the engine reads as unset. A 0 is told apart from unset by explicitSwappinessZero.
	if swappiness := int64(svc.MemSwappiness); swappiness > 0 {
		out.Limits.MemorySwappiness = &swappiness
	}

	var err error
	if err = translateDeploy(name, svc.Deploy, out); err != nil {
		return err
	}
	if out.NoNewPrivileges, err = composeSecurityOpt(name, svc.SecurityOpt); err != nil {
		return err
	}
	for _, entry := range svc.Tmpfs {
		target, opts, _ := strings.Cut(entry, ":")
		out.Mounts = append(out.Mounts, Mount{Type: MountTmpfs, Target: target, Options: opts})
	}
	return nil
}

// NOTE: Rounded to a thousandth of a CPU, since cpus is a 32-bit float.
func nanoCPUs(cpus float32) int64 {
	return int64(math.Round(float64(cpus)*1000)) * 1_000_000
}

// NOTE: The loader already rejects a deploy value that disagrees with its top-level twin, so the first one set wins.
func translateDeploy(name string, d *types.DeployConfig, out *Service) error {
	if d == nil {
		return nil
	}
	if ignored := swarmSettings(*d); len(ignored) > 0 {
		slog.Warn("Ignoring compose deploy settings other than resources", "service", name, "fields", ignored)
	}

	if l := d.Resources.Limits; l != nil {
		if err := checkDeployResource(name, "limits", l); err != nil {
			return err
		}
		if out.Limits.Memory == 0 {
			out.Limits.Memory = int64(l.MemoryBytes)
		}
		if out.Limits.NanoCPUs == 0 {
			out.Limits.NanoCPUs = nanoCPUs(float32(l.NanoCPUs))
		}
		if out.Limits.PidsLimit == 0 {
			out.Limits.PidsLimit = l.Pids
		}
	}
	if r := d.Resources.Reservations; r != nil {
		if err := checkDeployResource(name, "reservations", r); err != nil {
			return err
		}
		if r.NanoCPUs != 0 {
			return fmt.Errorf("compose service %q: %q is not supported because only a memory reservation exists outside swarm", name, "deploy.resources.reservations.cpus")
		}
		if out.Limits.MemoryReservation == 0 {
			out.Limits.MemoryReservation = int64(r.MemoryBytes)
		}
	}
	return nil
}

// NOTE: Placement and resources are structs, so they marshal as {} when unset; each field is judged on its own.
func swarmSettings(d types.DeployConfig) []string {
	d.Resources = types.Resources{}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	var set []string
	for k, v := range fields {
		if k != "resources" && !emptyJSON(v) {
			set = append(set, k)
		}
	}
	sort.Strings(set)
	return set
}

func checkDeployResource(name, section string, r *types.Resource) error {
	if len(r.Devices) > 0 {
		return fmt.Errorf("compose service %q: %q is not allowed because it exposes host devices", name, "deploy.resources."+section+".devices")
	}
	if len(r.GenericResources) > 0 {
		return fmt.Errorf("compose service %q: %q is not supported", name, "deploy.resources."+section+".generic_resources")
	}
	return nil
}

// NOTE: The loader types mem_swappiness as a plain number, so an explicit 0 reads like no setting; the raw model tells them apart.
func explicitSwappinessZero(ctx context.Context, path, server string) (map[string]bool, error) {
	opts, err := composeOptions(path, server)
	if err != nil {
		return nil, err
	}
	model, err := opts.LoadModel(ctx)
	if err != nil {
		return nil, err
	}
	services, _ := model["services"].(map[string]any)
	zero := map[string]bool{}
	for name, raw := range services {
		svc, _ := raw.(map[string]any)
		v, ok := svc["mem_swappiness"]
		if !ok {
			continue
		}
		if n, err := strconv.ParseInt(fmt.Sprint(v), 10, 64); err == nil && n == 0 {
			zero[name] = true
		}
	}
	return zero, nil
}

// NOTE: no-new-privileges is the only security option that removes privileges; the others turn protections off.
func composeSecurityOpt(service string, opts []string) (bool, error) {
	noNewPrivileges := false
	for _, opt := range opts {
		key, value, hasValue := strings.Cut(opt, ":")
		if !hasValue {
			key, value, hasValue = strings.Cut(opt, "=")
		}
		if key != "no-new-privileges" {
			return false, fmt.Errorf("compose service %q: %q entry %q is not allowed because it changes the container's security profile; only no-new-privileges is supported", service, "security_opt", opt)
		}
		switch {
		case !hasValue || value == "true":
			noNewPrivileges = true
		case value == "false":
		default:
			return false, fmt.Errorf("compose service %q: %q entry %q must end in true or false", service, "security_opt", opt)
		}
	}
	return noNewPrivileges, nil
}

// NOTE: The engine counts a tmpfs mode as octal, so it is printed that way.
func composeTmpfsOptions(t *types.ServiceVolumeTmpfs, readOnly bool) string {
	var opts []string
	if t != nil {
		if t.Size != 0 {
			opts = append(opts, fmt.Sprintf("size=%d", int64(t.Size)))
		}
		if t.Mode != 0 {
			opts = append(opts, fmt.Sprintf("mode=%o", t.Mode))
		}
	}
	if readOnly {
		opts = append(opts, "ro")
	}
	return strings.Join(opts, ",")
}
