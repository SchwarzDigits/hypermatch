package hypermatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func cidrP(v string) Pattern { return Pattern{Type: PatternCIDR, Value: v} }

// refCIDR reports whether the value v is an IP address inside pattern, a
// prefix or a single address. It uses package net, while the engine uses
// net/netip, so that the two are checked against each other.
func refCIDR(pattern, v string) bool {
	ip := net.ParseIP(v)
	if ip == nil {
		return false
	}
	_, network, err := net.ParseCIDR(pattern)
	if err != nil {
		host := net.ParseIP(pattern)
		if host == nil {
			return false
		}
		if v4 := host.To4(); v4 != nil {
			network = &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}
		} else {
			network = &net.IPNet{IP: host, Mask: net.CIDRMask(128, 128)}
		}
	}
	return network.Contains(ip)
}

var (
	// genPrefixes are valid values of CIDR patterns, and genAddresses are
	// event values for them, together with values that are not addresses.
	genPrefixes  = []string{"10.0.0.0/8", "10.1.0.0/16", "192.168.1.0/24", "10.1.2.3", "2001:db8::/32", "::ffff:10.0.0.0/104", "0.0.0.0/0", "::/0", "2001:DB8::1", "2001:db8:0::1/128", "10.1.2.3/8", "::ffff:0.0.0.0/96", "::ffff:0.0.0.0/80"}
	genAddresses = []string{"10.1.2.3", "10.2.0.1", "192.168.1.7", "192.168.2.1", "2001:db8::1", "2001:db9::1", "::ffff:10.1.2.3", "fe80::1%eth0", "1.2.3", "10.1.2.3/8", "2001:DB8::2"}
)

func TestCIDRSemantics(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		value   string
		want    bool
	}{
		{"inside", "10.0.0.0/8", "10.1.2.3", true},
		{"outside", "10.0.0.0/8", "11.1.2.3", false},
		{"prefix bits beyond the length are ignored", "10.1.2.3/8", "10.200.0.1", true},
		{"single address", "10.1.2.3", "10.1.2.3", true},
		{"single address, other value", "10.1.2.3", "10.1.2.4", false},
		{"everything", "0.0.0.0/0", "8.8.8.8", true},
		{"IPv6", "2001:db8::/32", "2001:db8:1::1", true},
		{"IPv6 outside", "2001:db8::/32", "2001:db9::1", false},
		{"IPv6 in upper case", "2001:DB8::/32", "2001:DB8::FF", true},
		{"IPv4 written as IPv6", "10.0.0.0/8", "::ffff:10.1.2.3", true},
		{"IPv4 prefix written as IPv6", "::ffff:10.0.0.0/104", "10.1.2.3", true},
		{"all IPv4 addresses written as IPv6", "::ffff:0.0.0.0/96", "8.8.8.8", true},
		{"IPv6 prefix of IPv4 addresses", "::ffff:0.0.0.0/80", "::ffff:8.8.8.8", false},
		{"IPv6 prefix of IPv4 addresses, IPv6 value", "::ffff:0.0.0.0/80", "::5", true},
		{"IPv4 does not match IPv6", "::/0", "10.1.2.3", false},
		{"IPv6 does not match IPv4", "0.0.0.0/0", "2001:db8::1", false},
		{"not an address", "10.0.0.0/8", "10.1.2", false},
		{"a prefix is not an address", "10.0.0.0/8", "10.1.2.3/8", false},
		{"an address with a zone", "fe80::/10", "fe80::1%eth0", false},
		{"leading zeros", "10.0.0.0/8", "010.1.2.3", false},
		{"text", "10.0.0.0/8", "localhost", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := ConditionSet{cond("ip", cidrP(tt.pattern))}
			event := []Property{prop("ip", tt.value)}
			h := New[int]()
			mustAdd(t, h, 1, rule...)
			if got := len(h.Match(event)) == 1; got != tt.want {
				t.Errorf("Match = %v, want %v", got, tt.want)
			}
			if got := refMatches(rule, event); got != tt.want {
				t.Errorf("reference = %v, want %v", got, tt.want)
			}
			if e, err := Explain(rule, event); err != nil || e.Matched != tt.want {
				t.Errorf("Explain = %v, %v, want %v", e.Matched, err, tt.want)
			}
		})
	}
}

// TestCIDRIndex adds many prefixes of different lengths to one path and
// checks every address against the reference.
func TestCIDRIndex(t *testing.T) {
	h := New[int]()
	ref := &refMatcher{}
	var patterns []string
	for i := range 256 {
		patterns = append(patterns,
			fmt.Sprintf("10.%d.0.0/16", i),
			fmt.Sprintf("10.1.%d.0/24", i),
			fmt.Sprintf("2001:db8:%x::/48", i),
			fmt.Sprintf("192.168.%d.%d", i%4, i))
	}
	patterns = append(patterns, "10.0.0.0/8", "0.0.0.0/0", "2001:db8::/32", "::/0")
	for i, p := range patterns {
		cs := ConditionSet{cond("ip", cidrP(p))}
		mustAdd(t, h, i, cs...)
		ref.add(i, cs)
	}
	for _, v := range []string{"10.1.2.3", "10.255.0.1", "11.0.0.1", "192.168.3.7", "192.168.3.8",
		"2001:db8:ff::1", "2001:db8:100::1", "2001:db9::1", "::ffff:10.1.200.1", "nope"} {
		event := []Property{prop("ip", v)}
		if got, want := h.Match(event), ref.match(event); !slices.Equal(got, want) {
			t.Errorf("Match(%s) = %v, want %v", v, got, want)
		}
	}
}

func TestCIDRValidation(t *testing.T) {
	for _, p := range []Pattern{
		cidrP(""),
		cidrP("10.0.0.0/33"),
		cidrP("10.0.0"),
		cidrP("fe80::1%eth0"),
		cidrP("fe80::/10%eth0"),
		cidrP("example.com"),
		{Type: PatternCIDR, Value: "10.0.0.0/8", Sub: []Pattern{equalsP("a")}},
	} {
		err := ValidateRule(ConditionSet{cond("ip", p)})
		if !errors.Is(err, ErrInvalidRule) || !strings.Contains(err.Error(), "[cidr]") {
			t.Errorf("ValidateRule(%s) = %v, want an error about [cidr]", fmtPattern(p), err)
		}
	}
}

func TestCIDRJSON(t *testing.T) {
	var rule ConditionSet
	if err := json.Unmarshal([]byte(`{"source.ip": {"cidr": "10.0.0.0/8"}}`), &rule); err != nil {
		t.Fatal(err)
	}
	if want := (ConditionSet{cond("source.ip", cidrP("10.0.0.0/8"))}); !reflect.DeepEqual(rule, want) {
		t.Errorf("Unmarshal = %s, want %s", fmtRule(rule), fmtRule(want))
	}
	if data, err := json.Marshal(rule); err != nil || string(data) != `{"source.ip":{"cidr":"10.0.0.0/8"}}` {
		t.Errorf("Marshal = %s, %v", data, err)
	}
	h := New[int]()
	mustAdd(t, h, 1, rule...)
	for event, want := range map[string]bool{
		`{"source": {"ip": "10.9.8.7"}}`:                true,
		`{"source": {"ip": ["192.0.2.1", "10.0.0.1"]}}`: true,
		`{"source": {"ip": "192.0.2.1"}}`:               false,
		`{"source": {"ip": 10}}`:                        false,
	} {
		got, err := h.MatchJSON([]byte(event))
		if err != nil || (len(got) == 1) != want {
			t.Errorf("MatchJSON(%s) = %v, %v, want a match: %v", event, got, err, want)
		}
	}
}

func TestCIDRAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates")
	}
	h := New[int]()
	mustAdd(t, h, 1, cond("ip", cidrP("10.0.0.0/8")))
	mustAdd(t, h, 2, cond("ip", cidrP("2001:db8::/32")))
	event := []Property{prop("ip", "10.1.2.3", "2001:db8::1", "::ffff:10.0.0.1", "nope")}
	dst := make([]int, 0, 4)
	if allocs := testing.AllocsPerRun(100, func() { dst = h.AppendMatches(dst[:0], event) }); allocs != 0 || len(dst) != 2 {
		t.Errorf("AppendMatches = %v with %v allocations per call, want [1 2] and none", dst, allocs)
	}
}

// refParseIP is parseIP as netip.ParseAddr defines it.
func refParseIP(s string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

var parseIPSeeds = []string{
	"", ".", ":", "::", ":::", "::1", "1::", "1:", ":1", "1:::2", "1::2::3", "0.0.0.0", "255.255.255.255",
	"256.0.0.0", "1.2.3", "1.2.3.4.5", "1.2.3.4.5.6", "1..2.3", ".1.2.3", "1.2.3.", "01.2.3.4", "0.0.0.00", "1.2.3.4 ",
	"1.2.3.4%eth0", "2001:db8::1", "2001:DB8:0:0:0:0:0:1", "1:2:3:4:5:6:7:8", "1:2:3:4:5:6:7:8:9",
	"1:2:3:4:5:6:7", "1:2:3:4:5:6:7::", "::1:2:3:4:5:6:7", "::1:2:3:4:5:6:7:8", "1:2:3:4::5:6:7:8",
	"12345::", "fffff::1", "ffff::1", "g::1", "::ffff:10.1.2.3", "::10.1.2.3", "1:2:3:4:5:6:1.2.3.4",
	"1:2:3:4:5:1.2.3.4", "1:2:3:4:5:6:7:1.2.3.4", "::1:2:3:4:5:6:1.2.3.4", "1::1.2.3.4", "::1.2.3.4:1",
	"::1.2.3", "::01.2.3.4", "1234.1.2.3", "ab.1.2.3", "::ab.1.2.3", "fe80::1%eth0", "fe80::1%", "-",
	"unknown", "nope", "12:30:00", "aa:bb:cc:dd:ee:ff", "10.1.2.3/8", "::ffff:1.2.3.4:5", ":1::",
	"::1:2:3:4:5:6:7:1.2.3.4", "1:2:3:4:5:6:7:8::", "::ffff:255.255.255.255", "0:0:0:0:0:0:0:0",
}

func TestParseIP(t *testing.T) {
	for _, s := range slices.Concat(parseIPSeeds, genAddresses, genPrefixes) {
		got, ok := parseIP([]byte(s))
		if want, wantOK := refParseIP(s); ok != wantOK || got != want {
			t.Errorf("parseIP(%q) = %v, %v, want %v, %v", s, got, ok, want, wantOK)
		}
	}
}

func FuzzParseIP(f *testing.F) {
	for _, s := range parseIPSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, ok := parseIP([]byte(s))
		if want, wantOK := refParseIP(s); ok != wantOK || got != want {
			t.Errorf("parseIP(%q) = %v, %v, want %v, %v", s, got, ok, want, wantOK)
		}
	})
}
