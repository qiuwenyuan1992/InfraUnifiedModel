package adapter

import "testing"

func TestNormalizePortNameRemovesUnicodeWhitespace(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"unchanged", "FHGigabitEthernet0/35", "FHGigabitEthernet0/35"},
		{"spaces", " FHGigabitEthernet 0 /35 ", "FHGigabitEthernet0/35"},
		{"tabs_and_lines", "\tFHGigabitEthernet\t0/\n35\r", "FHGigabitEthernet0/35"},
		{"unicode", "\u00a0FHGigabitEthernet\u20030/\u202f35\u3000", "FHGigabitEthernet0/35"},
		{"all_unicode_space", "\u0009\u000a\u000b\u000c\u000d\u0020\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000", ""},
		{"non_whitespace", "Eth\u200b0/35", "Eth\u200b0/35"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizePortName(tc.input); got != tc.want {
				t.Fatalf("NormalizePortName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
