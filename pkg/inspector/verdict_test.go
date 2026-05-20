package inspector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantKind    VerdictKind
		wantPayload string
		wantErr     bool
	}{
		{
			name:     "bare done",
			output:   "VERDICT: done",
			wantKind: VerdictDone,
		},
		{
			name:        "reject with yelling payload",
			output:      "VERDICT: reject | YOU FORGOT THE TESTS, ADD THEM",
			wantKind:    VerdictReject,
			wantPayload: "YOU FORGOT THE TESTS, ADD THEM",
		},
		{
			name:        "update with explanation",
			output:      "VERDICT: update | the task references an API that does not exist",
			wantKind:    VerdictUpdate,
			wantPayload: "the task references an API that does not exist",
		},
		{
			name:     "reject with no payload",
			output:   "VERDICT: reject",
			wantKind: VerdictReject,
		},
		{
			name:        "verdict line embedded in surrounding text",
			output:      "I reviewed the diff carefully.\nSome reasoning here.\nVERDICT: reject | MISSING ERROR HANDLING\n",
			wantKind:    VerdictReject,
			wantPayload: "MISSING ERROR HANDLING",
		},
		{
			name:        "kind is case-insensitive and normalized",
			output:      "VERDICT: DONE",
			wantKind:    VerdictDone,
			wantPayload: "",
		},
		{
			name:        "extra whitespace around pipe and payload",
			output:      "VERDICT:   reject   |    too sloppy   ",
			wantKind:    VerdictReject,
			wantPayload: "too sloppy",
		},
		{
			name:    "malformed - no verdict line",
			output:  "looks good to me, ship it",
			wantErr: true,
		},
		{
			name:    "malformed - unknown kind",
			output:  "VERDICT: maybe | not sure",
			wantErr: true,
		},
		{
			name:    "empty output",
			output:  "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := parseVerdict(tc.output)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantKind, v.Kind)
			assert.Equal(t, tc.wantPayload, v.Payload)
		})
	}
}
