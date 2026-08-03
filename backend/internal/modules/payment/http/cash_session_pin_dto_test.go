package http

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/payment/service"
)

// TestToParticipantResponse_WireShape pins the participant list's field
// names and — just as important — proves email never rides along.
// service.CashSessionPinService.ListParticipants intentionally resolves the
// person's email via identitypub.PersonReader.GetByID internally (Person
// carries it), but CashSessionParticipantView never stores it and this DTO
// never reads it: this is the regression test for that field-level cut
// (ADR-AUTH-001 layer 4).
func TestToParticipantResponse_WireShape(t *testing.T) {
	personID := uuid.New()

	body, err := json.Marshal(toParticipantResponse(service.CashSessionParticipantView{
		PersonID: personID,
		FullName: "Ayşe Yılmaz",
		HasPin:   true,
		Locked:   false,
	}))
	require.NoError(t, err)

	var asMap map[string]any
	require.NoError(t, json.Unmarshal(body, &asMap))

	for _, key := range []string{"person_id", "full_name", "has_pin", "locked"} {
		_, ok := asMap[key]
		assert.Truef(t, ok, "expected snake_case key %q in response body: %s", key, body)
	}
	assert.Len(t, asMap, 4, "response must carry exactly these 4 fields — no email, no other person field")

	for _, forbidden := range []string{"email", "Email", "e_mail"} {
		_, ok := asMap[forbidden]
		assert.Falsef(t, ok, "response must never carry %q", forbidden)
	}
	assert.NotContains(t, string(body), "example", "no email-shaped value should ever appear in this response")
}

func TestToParticipantResponse_FieldValues(t *testing.T) {
	personID := uuid.New()

	resp := toParticipantResponse(service.CashSessionParticipantView{
		PersonID: personID,
		FullName: "Mehmet Demir",
		HasPin:   false,
		Locked:   true,
	})

	assert.Equal(t, personID, resp.PersonID)
	assert.Equal(t, "Mehmet Demir", resp.FullName)
	assert.False(t, resp.HasPin)
	assert.True(t, resp.Locked)
}
