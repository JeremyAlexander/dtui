package catalog

import "testing"

func TestEchoCommandQuotesEveryValue(t *testing.T) {
	tests := []struct {
		operand string
		flags   []string
		want    string
	}{
		{"bp-0001", []string{"--decode-snapshots"}, `dtctl get things bp-0001 --decode-snapshots`},
		{"fetch logs", nil, `dtctl get things 'fetch logs'`},
		{`it's`, nil, `dtctl get things 'it'\''s'`},
		{"", nil, `dtctl get things ''`},
		{"a\n  b", nil, "dtctl get things 'a\n  b'"},
		{"$(id)", []string{"-S", "`id`"}, "dtctl get things '$(id)' -S '`id`'"},
		// A value that starts with a dash would be read as a flag: it goes
		// last, behind the end-of-options marker.
		{"-rf", []string{"-S", "uid-a"}, `dtctl get things -S uid-a -- -rf`},
		{"uid", []string{"-S", "-x"}, `dtctl get things uid -S -x`},
		// zsh replaces a word that starts with an equals sign by the path of
		// the command it names, so such a value is quoted.
		{"=ls", []string{"-S", "=id"}, `dtctl get things '=ls' -S '=id'`},
		{"a=b", nil, `dtctl get things 'a=b'`},
	}
	for _, tt := range tests {
		if got := EchoCommand("dtctl get things", tt.operand, tt.flags...); got != tt.want {
			t.Errorf("EchoCommand(%q, %q) =\n  %s\nwant\n  %s", tt.operand, tt.flags, got, tt.want)
		}
	}
}
