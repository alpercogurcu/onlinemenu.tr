package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"onlinemenu.tr/internal/modules/catalog/domain"
)

func TestOrderChannel_Valid(t *testing.T) {
	tests := []struct {
		channel domain.OrderChannel
		want    bool
	}{
		{domain.ChannelDineIn, true},
		{domain.ChannelTakeaway, true},
		{domain.ChannelDelivery, true},
		{"unknown", false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, tt.channel.Valid(), "channel %q", tt.channel)
	}
}

func TestSelectionType_Valid(t *testing.T) {
	tests := []struct {
		st   domain.SelectionType
		want bool
	}{
		{domain.SelectionSingle, true},
		{domain.SelectionMultiple, true},
		{"none", false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, tt.st.Valid(), "selection type %q", tt.st)
	}
}
