package safefetch

import "net/netip"

func blocked(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip.Is4() {
		octets := ip.As4()
		if octets[0] == 0 || octets[0] >= 240 {
			return true
		}
		if octets[0] == 100 && octets[1] >= 64 && octets[1] <= 127 {
			return true
		}
		if octets[0] == 192 && octets[1] == 0 && octets[2] == 0 {
			return true
		}
	}
	if ip.Is6() {
		if documentation6.Contains(ip) || ip == awsMetadata6 {
			return true
		}
	}
	return false
}

var (
	documentation6 = netip.MustParsePrefix("2001:db8::/32")
	awsMetadata6   = netip.MustParseAddr("fd00:ec2::254")
)
