package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"onlinemenu.tr/internal/modules/catalog/domain"
	pub "onlinemenu.tr/internal/modules/catalog/public"
)

func TestRequireName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", wantErr: true},
		{name: "whitespace only", input: "   ", wantErr: true},
		{name: "whitespace and tabs", input: "\t \n", wantErr: true},
		{name: "already trimmed", input: "Acı Sos", want: "Acı Sos"},
		{name: "leading and trailing whitespace trimmed", input: "  Acı Sos  ", want: "Acı Sos"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requireName(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				var ve *pub.ValidationError
				assert.True(t, errors.As(err, &ve), "expected *pub.ValidationError, got %T", err)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// These tests prove that empty/whitespace names are rejected before the
// service touches the database, by exercising the exported entry points on
// a zero-value service (db is nil — a nil pointer dereference would panic
// if validation didn't short-circuit first).

func TestModifierService_CreateModifier_RejectsBlankName(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "whitespace", in: "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ModifierService{}
			_, err := s.CreateModifier(context.Background(), uuid.New(), domain.Modifier{Name: tt.in})
			require.Error(t, err)
			var ve *pub.ValidationError
			assert.True(t, errors.As(err, &ve), "expected *pub.ValidationError, got %T", err)
		})
	}
}

func TestModifierService_UpdateModifier_RejectsBlankName(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "whitespace", in: "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ModifierService{}
			_, err := s.UpdateModifier(context.Background(), uuid.New(), domain.Modifier{ID: uuid.New(), Name: tt.in})
			require.Error(t, err)
			var ve *pub.ValidationError
			assert.True(t, errors.As(err, &ve), "expected *pub.ValidationError, got %T", err)
		})
	}
}

func TestModifierService_CreateGroup_RejectsBlankName(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "whitespace", in: "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ModifierService{}
			_, err := s.CreateGroup(context.Background(), uuid.New(), domain.ModifierGroup{Name: tt.in})
			require.Error(t, err)
			var ve *pub.ValidationError
			assert.True(t, errors.As(err, &ve), "expected *pub.ValidationError, got %T", err)
		})
	}
}

func TestModifierService_UpdateGroup_RejectsBlankName(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "whitespace", in: "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ModifierService{}
			_, err := s.UpdateGroup(context.Background(), uuid.New(), domain.ModifierGroup{ID: uuid.New(), Name: tt.in})
			require.Error(t, err)
			var ve *pub.ValidationError
			assert.True(t, errors.As(err, &ve), "expected *pub.ValidationError, got %T", err)
		})
	}
}

func TestProductService_Create_RejectsBlankName(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "whitespace", in: "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProductService{}
			_, err := s.Create(context.Background(), uuid.New(), domain.Product{Name: tt.in})
			require.Error(t, err)
			var ve *pub.ValidationError
			assert.True(t, errors.As(err, &ve), "expected *pub.ValidationError, got %T", err)
		})
	}
}

func TestProductService_Update_RejectsBlankName(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "whitespace", in: "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ProductService{}
			_, err := s.Update(context.Background(), uuid.New(), domain.Product{ID: uuid.New(), Name: tt.in})
			require.Error(t, err)
			var ve *pub.ValidationError
			assert.True(t, errors.As(err, &ve), "expected *pub.ValidationError, got %T", err)
		})
	}
}
