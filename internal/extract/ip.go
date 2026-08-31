package extract

import "net/netip"

// ipVersionOf returns ipv4 | ipv6 | domain for a host string. an ipv4-mapped
// address is ipv6, matching ip_address("::ffff:1.2.3.4").version == 6.
func ipVersionOf(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "domain"
	}
	if a.Is4() {
		return "ipv4"
	}
	return "ipv6"
}

// bogus is the union of python's is_loopback / is_private / is_link_local /
// is_multicast / is_reserved / is_unspecified, spelled out because go's
// netip.Addr.IsPrivate is only RFC1918 + fc00::/7 while python's is_private
// carries the whole IANA special-purpose list.
//
// two addresses drift: python 3.12.4 added 192.0.0.9 and 192.0.0.10 as
// exceptions to is_private, so it would keep them. they live in IANA
// protocol-assignment space and will never be a proxy.
var bogus = mustPrefixes(
	// ipv4. 100.64.0.0/10 is deliberately absent — is_private does not cover
	// cgnat, only is_global does, and nothing here calls is_global.
	"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.0.0.0/29", "192.0.0.170/31", "192.0.2.0/24",
	"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4",

	// ipv6 private
	"::1/128", "::/128", "::ffff:0:0/96", "100::/64", "2001::/23",
	"2001:2::/48", "2001:db8::/32", "2001:10::/28", "fc00::/7", "fe80::/10",
	// ipv6 multicast
	"ff00::/8",
	// ipv6 reserved. between them these leave only 2000::/3 routable.
	"::/8", "100::/8", "200::/7", "400::/6", "800::/5", "1000::/4",
	"4000::/3", "6000::/3", "8000::/3", "a000::/3", "c000::/3", "e000::/4",
	"f000::/5", "f800::/6", "fe00::/9",
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// isBogusIP rejects the addresses no proxy can legitimately live on. a host
// that is not an ip at all is not bogus — domains are allowed through.
func isBogusIP(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	// no Unmap: python tests an ipv4-mapped address against the v6 lists
	// only, where ::ffff:0:0/96 catches every one of them. netip.Prefix
	// matches on address family, so the split falls out for free.
	for _, p := range bogus {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
