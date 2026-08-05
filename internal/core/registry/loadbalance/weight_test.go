package loadbalance

import "testing"

func TestParseNodeWeight(t *testing.T) {
	cases := []struct {
		path       string
		wantWeight int32
		wantOK     bool
	}{
		{"/services/svc/node-0@1.sock", 1, true},
		{"/services/svc/node-1@3.sock", 3, true},
		{"/services/svc/node-6@10.sock", 10, true},
		{"/services/svc/node-2.sock", 1, true},
		{"/services/svc/node-7@100.sock", 100, true},
		{"/services/svc/node-3@0.sock", 1, false},
		{"/services/svc/node-4@-5.sock", 1, false},
		{"/services/svc/node-5@abc.sock", 1, false},
		{"/services/svc/node-8@101.sock", 1, false},
		{"node-0@1.sock", 1, true},
	}

	for _, c := range cases {
		gotWeight, gotOK := ParseNodeWeight(c.path)
		if gotWeight != c.wantWeight || gotOK != c.wantOK {
			t.Errorf("ParseNodeWeight(%q) = (%d, %v), want (%d, %v)", c.path, gotWeight, gotOK, c.wantWeight, c.wantOK)
		}
	}
}
