package containermcp

import (
	"net"
	"net/netip"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func TestApplyOptions_MapsEveryFieldToTheEngineConfig(t *testing.T) {
	t.Parallel()

	svc := Service{
		CapDrop:         []string{"ALL"},
		NoNewPrivileges: true,
		ReadOnly:        true,
		Hostname:        "mcp-app",
		Domainname:      "corp.test",
		StopSignal:      "SIGINT",
		DNS:             []string{"1.1.1.1", "2606:4700:4700::1111"},
		DNSSearch:       []string{"corp.test"},
		DNSOptions:      []string{"ndots:2"},
		Limits: Limits{
			Memory: 1 << 28, MemoryReservation: 1 << 27, MemorySwap: 1 << 29,
			NanoCPUs: 500_000_000, CPUShares: 512, CPUPeriod: 100000, CPUQuota: 50000,
			CPUSet: "0-1", PidsLimit: 100, MemorySwappiness: new(int64(0)),
		},
	}
	cfg, host := &container.Config{}, &container.HostConfig{}
	require.NoError(t, applyOptions(cfg, host, svc))

	require.Equal(t, "mcp-app", cfg.Hostname)
	require.Equal(t, "corp.test", cfg.Domainname)
	require.Equal(t, "SIGINT", cfg.StopSignal)
	require.Equal(t, []string{"ALL"}, host.CapDrop)
	require.True(t, host.ReadonlyRootfs)
	require.Equal(t, []string{"no-new-privileges"}, host.SecurityOpt)
	require.Equal(t, []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}, host.DNS)
	require.Equal(t, []string{"corp.test"}, host.DNSSearch)
	require.Equal(t, []string{"ndots:2"}, host.DNSOptions)
	require.Equal(t, int64(1<<28), host.Memory)
	require.Equal(t, int64(1<<27), host.MemoryReservation)
	require.Equal(t, int64(1<<29), host.MemorySwap)
	require.Equal(t, int64(500_000_000), host.NanoCPUs)
	require.Equal(t, int64(512), host.CPUShares)
	require.Equal(t, int64(100000), host.CPUPeriod)
	require.Equal(t, int64(50000), host.CPUQuota)
	require.Equal(t, "0-1", host.CpusetCpus)
	require.NotNil(t, host.PidsLimit)
	require.Equal(t, int64(100), *host.PidsLimit)
	require.NotNil(t, host.MemorySwappiness, "an explicit zero is sent to the engine")
	require.Equal(t, int64(0), *host.MemorySwappiness)
}

func TestApplyOptions_ZeroValuesLeaveTheEngineDefaults(t *testing.T) {
	t.Parallel()

	cfg, host := &container.Config{}, &container.HostConfig{}
	require.NoError(t, applyOptions(cfg, host, Service{}))

	require.Empty(t, host.SecurityOpt, "no-new-privileges is only sent when asked for")
	require.Nil(t, host.PidsLimit, "a pids limit of zero means unlimited, so it is left unset")
	require.Nil(t, host.MemorySwappiness, "an unset swappiness keeps the engine default")
	require.False(t, host.ReadonlyRootfs)
	require.Empty(t, host.DNS)
}

func TestApplyOptions_RejectsADNSEntryThatIsNotAnAddress(t *testing.T) {
	t.Parallel()

	err := applyOptions(&container.Config{}, &container.HostConfig{}, Service{DNS: []string{"dns.local"}})
	require.ErrorContains(t, err, "dns entry")
}

func TestHijackedAttachment_ClosingTwiceIsNotAnError(t *testing.T) {
	path := shortSocket(t)
	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "unix", path)
	require.NoError(t, err)

	att := &hijackedAttachment{resp: client.NewHijackedResponse(conn, "")}
	require.NoError(t, att.Close())
	require.NoError(t, att.CloseWrite(), "the SDK closes the writer after the reader already closed the session")
	require.NoError(t, att.Close())
}
