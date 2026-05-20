package oracle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProposal(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantOld string
		wantNew string
		wantErr bool
	}{
		{
			name:    "simple old/new",
			output:  "OLD: use the FooBar API\nNEW: use the FooBaz API",
			wantOld: "use the FooBar API",
			wantNew: "use the FooBaz API",
		},
		{
			name:    "tolerates surrounding reasoning",
			output:  "The task is infeasible because X.\nOLD: call deprecated()\nNEW: call current()\nThat should fix it.",
			wantOld: "call deprecated()",
			wantNew: "call current()",
		},
		{
			name:    "new may be empty (deletion)",
			output:  "OLD: remove this clause\nNEW:",
			wantOld: "remove this clause",
			wantNew: "",
		},
		{
			name:    "missing NEW errors",
			output:  "OLD: something",
			wantErr: true,
		},
		{
			name:    "missing OLD errors",
			output:  "NEW: something",
			wantErr: true,
		},
		{
			name:    "empty OLD errors (nothing to match)",
			output:  "OLD:\nNEW: replacement",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old, newStr, err := parseProposal(tc.output)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOld, old)
			assert.Equal(t, tc.wantNew, newStr)
		})
	}
}
