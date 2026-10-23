package semver

import "testing"

func TestParseValid(t *testing.T) {
	for _, s := range []string{
		"0.0.0", "1.0.0", "1.4.0", "10.20.30", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-0.3.7",
		"1.0.0-x.7.z.92", "1.0.0-x-y-z.-", "1.0.0+20130313144700", "1.0.0-beta+exp.sha.5114f85",
		"1.0.0+001", "1.0.0-rc.1+build.1", "1.2.3-0", "1.0.0-alpha-a.b-c-somethinglong",
	} {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q) rejected: %v", s, err)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, s := range []string{
		"", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "v1.2.3", "1.2.3-", "1.2.3+",
		"1.2.3-01", "1.2.3-alpha..1", "1.2.3-alpha_beta", "1.2.3 ", " 1.2.3", "-1.2.3", "1.2.-3",
		"1.2.3-+", "1.2.3+a..b", "1.2.3-é", "a.b.c", "1.2.3-1.01", "1.0.0+a_b",
		"1.2.3-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) accepted", s)
		}
	}
}

func TestPrecedenceChainFromSemverOrg(t *testing.T) {
	chain := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "2.0.0", "2.1.0", "2.1.1",
	}
	for i := range chain {
		for j := range chain {
			got, err := CompareStrings(chain[i], chain[j])
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", chain[i], chain[j], got, want)
			}
		}
	}
}

func TestPrecedenceDetails(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.9.0", "1.10.0", -1},
		{"1.0.0+a", "1.0.0+b", 0},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"99999999999999999999999.0.0", "99999999999999999999998.0.0", 1},
		{"1.0.0-2", "1.0.0-10", -1},
		{"1.0.0-alpha", "1.0.0-Alpha", 1},
	}
	for _, c := range cases {
		got, err := CompareStrings(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("Compare(%s, %s) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
}
