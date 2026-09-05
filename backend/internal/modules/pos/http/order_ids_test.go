package http

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseOrderIDs documents the batch endpoint's input contract (see
// parseOrderIDs / listOrdersByIDs): every rejection here is a 422, and the
// accepted cases are what a KDS client actually sends — a comma-joined id
// list that may repeat an id when the same order appears twice on the board.
func TestParseOrderIDs(t *testing.T) {
	first := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	second := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	tests := []struct {
		name    string
		raw     string
		want    []uuid.UUID
		wantErr string
	}{
		{
			name: "single id",
			raw:  first.String(),
			want: []uuid.UUID{first},
		},
		{
			name: "multiple ids keep first-seen order",
			raw:  first.String() + "," + second.String(),
			want: []uuid.UUID{first, second},
		},
		{
			name: "surrounding whitespace tolerated",
			raw:  " " + first.String() + " , " + second.String() + " ",
			want: []uuid.UUID{first, second},
		},
		{
			name: "duplicates collapsed",
			raw:  first.String() + "," + second.String() + "," + first.String(),
			want: []uuid.UUID{first, second},
		},
		{
			name: "empty segments ignored",
			raw:  first.String() + ",,",
			want: []uuid.UUID{first},
		},
		{
			name:    "missing parameter",
			raw:     "",
			wantErr: "ids query parameter is required",
		},
		{
			name:    "only separators",
			raw:     ",,,",
			wantErr: "ids query parameter is required",
		},
		{
			name:    "malformed id",
			raw:     first.String() + ",not-a-uuid",
			wantErr: `invalid id "not-a-uuid"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOrderIDs(tt.raw)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseOrderIDs_Limit pins the boundary: exactly maxOrderIDsPerRequest
// distinct ids is accepted, one more is rejected — and a request that only
// exceeds the cap because it repeats ids is accepted, since the dedup runs
// before the count (a client rendering the same order twice must not be
// punished for it).
func TestParseOrderIDs_Limit(t *testing.T) {
	ids := make([]uuid.UUID, maxOrderIDsPerRequest+1)
	for i := range ids {
		ids[i] = uuid.New()
	}

	atLimit, err := parseOrderIDs(joinIDs(ids[:maxOrderIDsPerRequest]))
	require.NoError(t, err)
	assert.Len(t, atLimit, maxOrderIDsPerRequest)

	_, err = parseOrderIDs(joinIDs(ids))
	require.Error(t, err)
	assert.Equal(t, "ids exceeds the 200 id limit", err.Error())

	// Same id repeated past the cap: 201 segments, 1 distinct id.
	repeated := make([]uuid.UUID, maxOrderIDsPerRequest+1)
	for i := range repeated {
		repeated[i] = ids[0]
	}
	deduped, err := parseOrderIDs(joinIDs(repeated))
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{ids[0]}, deduped)
}

func joinIDs(ids []uuid.UUID) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id.String()
	}
	return out
}
