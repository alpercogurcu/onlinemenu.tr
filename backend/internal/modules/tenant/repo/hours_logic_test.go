package repo

// White-box unit tests for the pure scheduling helpers in hours_repo.go.
// These need no database and run as fast unit tests; they previously had
// zero coverage even though IsOpenAt's correctness depends entirely on them.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pub "onlinemenu.tr/internal/modules/tenant/public"
)

func tod(h, m int) *pub.TimeOfDay {
	return &pub.TimeOfDay{Hour: h, Minute: m}
}

func TestIsWithinWindow(t *testing.T) {
	tests := []struct {
		name            string
		at              time.Time
		open, close     *pub.TimeOfDay
		crossesMidnight bool
		isClosed        bool
		want            bool
	}{
		{
			name: "closed day always false regardless of hours",
			at:   time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC),
			open: tod(9, 0), close: tod(22, 0),
			isClosed: true,
			want:     false,
		},
		{
			name: "nil open time is closed",
			at:   time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC),
			open: nil, close: tod(22, 0),
			want: false,
		},
		{
			name: "nil close time is closed",
			at:   time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC),
			open: tod(9, 0), close: nil,
			want: false,
		},
		{
			name: "normal window inside",
			at:   time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC),
			open: tod(9, 0), close: tod(22, 0),
			want: true,
		},
		{
			name: "normal window at open boundary is inside (inclusive)",
			at:   time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC),
			open: tod(9, 0), close: tod(22, 0),
			want: true,
		},
		{
			name: "normal window at close boundary is outside (exclusive)",
			at:   time.Date(2026, 1, 5, 22, 0, 0, 0, time.UTC),
			open: tod(9, 0), close: tod(22, 0),
			want: false,
		},
		{
			name: "normal window before open",
			at:   time.Date(2026, 1, 5, 8, 59, 0, 0, time.UTC),
			open: tod(9, 0), close: tod(22, 0),
			want: false,
		},
		{
			name: "crosses-midnight window, evening side",
			at:   time.Date(2026, 1, 5, 23, 30, 0, 0, time.UTC),
			open: tod(22, 0), close: tod(2, 0),
			crossesMidnight: true,
			want:            true,
		},
		{
			name: "crosses-midnight window, early-morning side",
			at:   time.Date(2026, 1, 5, 1, 0, 0, 0, time.UTC),
			open: tod(22, 0), close: tod(2, 0),
			crossesMidnight: true,
			want:            true,
		},
		{
			name: "crosses-midnight window, mid-afternoon is outside",
			at:   time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC),
			open: tod(22, 0), close: tod(2, 0),
			crossesMidnight: true,
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWithinWindow(tt.at, tt.open, tt.close, tt.crossesMidnight, tt.isClosed)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsoWeekdayRoundTrip(t *testing.T) {
	// ISO 8601: Monday=1 ... Sunday=7. Go: Sunday=0 ... Saturday=6.
	tests := []struct {
		goDay  time.Weekday
		isoDay int
	}{
		{time.Monday, 1},
		{time.Tuesday, 2},
		{time.Wednesday, 3},
		{time.Thursday, 4},
		{time.Friday, 5},
		{time.Saturday, 6},
		{time.Sunday, 7},
	}

	for _, tt := range tests {
		t.Run(tt.goDay.String(), func(t *testing.T) {
			assert.Equal(t, tt.isoDay, isoWeekday(tt.goDay))
			assert.Equal(t, tt.goDay, goWeekday(tt.isoDay))
		})
	}
}

func TestParseTimeOfDay(t *testing.T) {
	t.Run("empty string is nil, no error", func(t *testing.T) {
		got, err := parseTimeOfDay("")
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("HH:MM", func(t *testing.T) {
		got, err := parseTimeOfDay("09:30")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, pub.TimeOfDay{Hour: 9, Minute: 30}, *got)
	})

	t.Run("HH:MM:SS", func(t *testing.T) {
		got, err := parseTimeOfDay("23:59:59")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, pub.TimeOfDay{Hour: 23, Minute: 59}, *got)
	})

	t.Run("missing minute component errors", func(t *testing.T) {
		_, err := parseTimeOfDay("9")
		require.Error(t, err)
	})

	t.Run("non-numeric hour errors", func(t *testing.T) {
		_, err := parseTimeOfDay("ab:00")
		require.Error(t, err)
	})

	t.Run("hour out of range errors", func(t *testing.T) {
		_, err := parseTimeOfDay("25:00")
		require.Error(t, err)
	})

	t.Run("minute out of range errors", func(t *testing.T) {
		_, err := parseTimeOfDay("10:60")
		require.Error(t, err)
	})
}
