package hypermatch

import (
	"net/netip"
	"slices"
	"sync/atomic"
)

// CIDR patterns match values that are IP addresses inside a prefix, such as
// 10.0.0.0/8 or 2001:db8::/32. IPv4 addresses written as IPv6 addresses,
// such as ::ffff:10.1.2.3, count as IPv4 addresses. Values that are not IP
// addresses, including addresses with a zone, never match.

// parsePrefix parses the value of a CIDR pattern: a prefix, or an address,
// which stands for itself. The result is masked, and IPv4 prefixes written
// as IPv6 prefixes become IPv4 prefixes.
func parsePrefix(s string) (netip.Prefix, bool) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, false
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
		p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
	}
	return p.Masked(), true
}

// parseIP parses v as an IP address, the way CIDR patterns see values. It
// accepts the addresses without zone that netip.ParseAddr accepts, but it
// reports values that are not addresses without allocating an error, since
// such values, like "-" or "unknown", are common in events.
func parseIP(v []byte) (netip.Addr, bool) {
	for _, c := range v {
		switch c {
		case '.':
			if ip, ok := parseIPv4(v); ok {
				return netip.AddrFrom4(ip), true
			}
			return netip.Addr{}, false
		case ':':
			if ip, ok := parseIPv6(v); ok {
				return netip.AddrFrom16(ip).Unmap(), true
			}
			return netip.Addr{}, false
		}
	}
	return netip.Addr{}, false
}

// parseIPv4 parses s as four decimal numbers from 0 to 255 separated by
// dots, without leading zeros.
func parseIPv4(s []byte) (ip [4]byte, ok bool) {
	n, val, digits := 0, 0, 0
	for _, c := range s {
		switch {
		case '0' <= c && c <= '9':
			if digits == 1 && val == 0 {
				return ip, false
			}
			val = val*10 + int(c-'0')
			digits++
			if val > 255 {
				return ip, false
			}
		case c == '.' && digits > 0 && n < 3:
			ip[n] = byte(val)
			n, val, digits = n+1, 0, 0
		default:
			return ip, false
		}
	}
	if n != 3 || digits == 0 {
		return ip, false
	}
	ip[3] = byte(val)
	return ip, true
}

// parseIPv6 parses s as groups of one to four hex digits separated by
// colons, where "::" stands for one or more groups of zeros and an IPv4
// address may take the place of the last two groups.
func parseIPv6(s []byte) (ip [16]byte, ok bool) {
	n := 0    // bytes of ip filled
	gap := -1 // where "::" stands in ip
	if len(s) >= 2 && s[0] == ':' && s[1] == ':' {
		gap, s = 0, s[2:]
	}
	for len(s) > 0 {
		if n == len(ip) {
			return ip, false
		}
		k, group := 0, 0
		for ; k < len(s); k++ {
			d := hexValue(s[k])
			if d < 0 {
				break
			}
			if k == 4 {
				return ip, false
			}
			group = group<<4 | d
		}
		if k == 0 {
			return ip, false
		}
		if k < len(s) && s[k] == '.' {
			if n > 12 || gap < 0 && n != 12 {
				return ip, false
			}
			v4, ok := parseIPv4(s)
			if !ok {
				return ip, false
			}
			copy(ip[n:], v4[:])
			n += 4
			break
		}
		ip[n], ip[n+1] = byte(group>>8), byte(group)
		n += 2
		s = s[k:]
		if len(s) == 0 {
			break
		}
		if s[0] != ':' || len(s) == 1 {
			return ip, false
		}
		s = s[1:]
		if s[0] == ':' {
			if gap >= 0 {
				return ip, false
			}
			gap, s = n, s[1:]
		}
	}
	switch {
	case gap < 0:
		return ip, n == len(ip)
	case n == len(ip):
		return ip, false // "::" stands for no group
	}
	shift := len(ip) - n
	copy(ip[gap+shift:], ip[gap:n])
	clear(ip[gap : gap+shift])
	return ip, true
}

// hexValue returns the value of the hex digit c, or -1.
func hexValue(c byte) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'a' <= c && c <= 'f':
		return int(c-'a') + 10
	case 'A' <= c && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// cidrIndex finds the CIDR leaves of a group that contain an address: for
// every distinct prefix length, one hash lookup of the masked address.
type cidrIndex struct {
	prefixes strMap[*leaf]               // by prefixKey
	lens     atomic.Pointer[cidrLengths] // copied on write
}

// cidrLengths holds the distinct lengths of the IPv4 and of the IPv6
// prefixes of an index, sorted.
type cidrLengths struct {
	v4, v6 []int
}

// prefixKey returns the key of the masked prefix of a with the given
// length: the length followed by the bytes of the address, in buf.
func prefixKey(buf *[17]byte, a netip.Addr, bits int) []byte {
	buf[0] = byte(bits)
	if a.Is4() {
		b := a.As4()
		return append(buf[:1], b[:]...)
	}
	b := a.As16()
	return append(buf[:1], b[:]...)
}

// add registers the leaf l for the valid, normalized prefix value. The
// prefix is published before its length, so a reader that sees the length
// finds the prefix. Writer only.
func (ci *cidrIndex) add(value string, l *leaf) {
	p, _ := parsePrefix(value)
	var buf [17]byte
	ci.prefixes.put(string(prefixKey(&buf, p.Addr(), p.Bits())), l)

	old := ci.lens.Load()
	var cur []int
	if old != nil {
		cur = old.v6
		if p.Addr().Is4() {
			cur = old.v4
		}
	}
	i, found := slices.BinarySearch(cur, p.Bits())
	if found {
		return
	}
	next := new(cidrLengths)
	if old != nil {
		*next = *old
	}
	grown := slices.Insert(slices.Clone(cur), i, p.Bits())
	if p.Addr().Is4() {
		next.v4 = grown
	} else {
		next.v6 = grown
	}
	ci.lens.Store(next)
}

// collect appends the leaves whose prefixes contain the address v to hits.
func (ci *cidrIndex) collect(v []byte, hits []*leaf) []*leaf {
	lens := ci.lens.Load()
	if lens == nil {
		return hits
	}
	a, ok := parseIP(v)
	if !ok {
		return hits
	}
	all := lens.v6
	if a.Is4() {
		all = lens.v4
	}
	var buf [17]byte
	for _, bits := range all {
		p, _ := a.Prefix(bits)
		if l, ok := ci.prefixes.getBytes(prefixKey(&buf, p.Addr(), bits)); ok {
			hits = append(hits, l)
		}
	}
	return hits
}
