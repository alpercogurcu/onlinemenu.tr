package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"onlinemenu.tr/internal/modules/identity/repo"
	"onlinemenu.tr/internal/platform/db"
	"onlinemenu.tr/internal/platform/eventbus"
)

const (
	tenantCreatedSubject = "tenant.created.v1"
	tenantCreatedDurable = "identity-tenant-created-v1"
)

var systemRoleKeys = []string{
	"cashier",
	"shift_manager",
	"driver",
	"kitchen",
	"bar",
	"manager",
}

type tenantCreatedPayload struct {
	TenantID string `json:"tenant_id"`
}

// Subscriber registers NATS consumers that the identity module reacts to.
type Subscriber struct {
	bus      *eventbus.Bus
	db       *db.Pool
	roleRepo *repo.RoleRepo
	logger   *zap.Logger
}

func NewSubscriber(bus *eventbus.Bus, pool *db.Pool, roleRepo *repo.RoleRepo, logger *zap.Logger) *Subscriber {
	return &Subscriber{bus: bus, db: pool, roleRepo: roleRepo, logger: logger}
}

func (s *Subscriber) Register() error {
	if err := s.bus.Subscribe(
		context.Background(),
		tenantCreatedSubject,
		tenantCreatedDurable,
		s.handleTenantCreated,
	); err != nil {
		return fmt.Errorf("identity/events: register tenant.created subscriber: %w", err)
	}
	return nil
}

func (s *Subscriber) handleTenantCreated(ctx context.Context, msg jetstream.Msg) error {
	var p tenantCreatedPayload
	if err := json.Unmarshal(msg.Data(), &p); err != nil {
		return fmt.Errorf("identity/events: unmarshal tenant.created: %w", err)
	}

	tenantID, err := uuid.Parse(p.TenantID)
	if err != nil {
		return fmt.Errorf("identity/events: parse tenant_id %q: %w", p.TenantID, err)
	}

	copied, err := s.seedTenantRoles(ctx, tenantID)
	if err != nil {
		return err
	}
	if copied == 0 {
		s.logger.Warn("identity/events: system roles already seeded",
			zap.String("tenant_id", tenantID.String()),
		)
	} else {
		s.logger.Info("identity/events: system roles seeded for tenant",
			zap.String("tenant_id", tenantID.String()),
			zap.Int("count", copied),
		)
	}
	return nil
}

// seedTenantRoles copies system role rows AND their permissions/field policies into
// the new tenant in a single transaction. ON CONFLICT DO NOTHING makes the operation
// safe under JetStream redelivery (ADR-DATA-002).
func (s *Subscriber) seedTenantRoles(ctx context.Context, tenantID uuid.UUID) (int, error) {
	// Clone role rows.
	const qRoles = `
		INSERT INTO roles (tenant_id, branch_id, name, system_key, is_system)
		SELECT $1, NULL, name, NULL, FALSE
		FROM roles
		WHERE tenant_id IS NULL AND system_key = ANY($2)
		ON CONFLICT (tenant_id, branch_id, name) DO NOTHING
		RETURNING id`

	// Clone role_permissions: join new tenant role by name to system role permissions.
	const qPerms = `
		INSERT INTO role_permissions (role_id, tenant_id, resource, action)
		SELECT nr.id, $1, rp.resource, rp.action
		FROM role_permissions rp
		JOIN roles sr ON sr.id = rp.role_id AND sr.tenant_id IS NULL AND sr.system_key = ANY($2)
		JOIN roles nr ON nr.tenant_id = $1 AND nr.name = sr.name AND nr.branch_id IS NULL
		ON CONFLICT (role_id, resource, action) DO NOTHING`

	// Clone role_field_policies: same join pattern.
	const qFields = `
		INSERT INTO role_field_policies (role_id, tenant_id, resource, field)
		SELECT nr.id, $1, rfp.resource, rfp.field
		FROM role_field_policies rfp
		JOIN roles sr ON sr.id = rfp.role_id AND sr.tenant_id IS NULL AND sr.system_key = ANY($2)
		JOIN roles nr ON nr.tenant_id = $1 AND nr.name = sr.name AND nr.branch_id IS NULL
		ON CONFLICT (role_id, resource, field) DO NOTHING`

	var newRoleCount int
	err := s.db.WithTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, qRoles, tenantID, systemRoleKeys)
		if err != nil {
			return fmt.Errorf("identity/events: clone roles: %w", err)
		}
		newRoleCount = int(ct.RowsAffected())

		if _, err = tx.Exec(ctx, qPerms, tenantID, systemRoleKeys); err != nil {
			return fmt.Errorf("identity/events: clone role_permissions: %w", err)
		}
		if _, err = tx.Exec(ctx, qFields, tenantID, systemRoleKeys); err != nil {
			return fmt.Errorf("identity/events: clone role_field_policies: %w", err)
		}
		return nil
	})
	return newRoleCount, err
}
